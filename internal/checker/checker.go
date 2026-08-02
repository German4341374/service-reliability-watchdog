package checker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

const maxResponseBody = 1024 * 1024

type Resolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type Checker struct {
	Resolver  Resolver
	Now       func() time.Time
	TLSConfig *tls.Config
}

func New() *Checker {
	return &Checker{Resolver: net.DefaultResolver, Now: time.Now}
}

func (c *Checker) Check(ctx context.Context, endpoint domain.Endpoint) domain.CheckResult {
	started := c.Now()
	result := domain.CheckResult{
		EndpointID: endpoint.ID,
		CheckedAt:  started.UTC(),
		State:      domain.StateUnknown,
		Message:    "check did not complete",
	}

	host, err := endpointHost(endpoint)
	if err != nil {
		return c.failed(result, started, fmt.Errorf("resolve endpoint host: %w", err))
	}
	dnsCtx, cancel := context.WithTimeout(ctx, endpoint.Timeout)
	_, err = c.Resolver.LookupHost(dnsCtx, host)
	cancel()
	if err != nil {
		return c.failed(result, started, fmt.Errorf("DNS resolution failed: %w", err))
	}
	result.DNSResolved = true

	switch endpoint.Type {
	case domain.CheckHTTP:
		result = c.checkHTTP(ctx, endpoint, result)
	case domain.CheckTCP:
		result = c.checkTCP(ctx, endpoint, result)
	case domain.CheckTLS:
		result = c.checkTLS(ctx, endpoint, result, host)
	default:
		return c.failed(result, started, fmt.Errorf("unsupported check type %q", endpoint.Type))
	}
	result.Latency = c.Now().Sub(started)
	if result.State == domain.StateHealthy && endpoint.LatencyTarget > 0 && result.Latency > endpoint.LatencyTarget {
		result.State = domain.StateDegraded
		result.Message = fmt.Sprintf("response exceeded latency target %s", endpoint.LatencyTarget)
	}
	return result
}

func (c *Checker) checkHTTP(ctx context.Context, endpoint domain.Endpoint, result domain.CheckResult) domain.CheckResult {
	requestCtx, cancel := context.WithTimeout(ctx, endpoint.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, endpoint.Method, endpoint.Address, nil)
	if err != nil {
		return c.failed(result, result.CheckedAt, fmt.Errorf("create HTTP request: %w", err))
	}
	for name, value := range endpoint.Headers {
		request.Header.Set(name, value)
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: endpoint.Timeout}).DialContext,
		TLSHandshakeTimeout: endpoint.Timeout,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     cloneTLSConfig(c.TLSConfig, ""),
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   endpoint.Timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("stopped after 3 redirects")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return c.failed(result, result.CheckedAt, fmt.Errorf("HTTP request failed: %w", err))
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	if response.TLS != nil {
		applyTLS(&result, response.TLS, endpoint.TLSWarnBefore, c.Now())
		if result.State == domain.StateUnavailable {
			return result
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil {
		return c.failed(result, result.CheckedAt, fmt.Errorf("read HTTP response: %w", err))
	}
	if len(body) > maxResponseBody {
		return c.failed(result, result.CheckedAt, errors.New("HTTP response exceeds 1 MiB inspection limit"))
	}
	if response.StatusCode != endpoint.ExpectedStatus {
		return c.failed(result, result.CheckedAt, fmt.Errorf("expected HTTP %d, received %d", endpoint.ExpectedStatus, response.StatusCode))
	}
	if endpoint.ExpectedText != "" && !strings.Contains(string(body), endpoint.ExpectedText) {
		return c.failed(result, result.CheckedAt, errors.New("expected response text was not found"))
	}
	if result.State != domain.StateDegraded {
		result.State = domain.StateHealthy
		result.Message = "HTTP check passed"
	}
	return result
}

func (c *Checker) checkTCP(ctx context.Context, endpoint domain.Endpoint, result domain.CheckResult) domain.CheckResult {
	dialCtx, cancel := context.WithTimeout(ctx, endpoint.Timeout)
	defer cancel()
	connection, err := (&net.Dialer{Timeout: endpoint.Timeout}).DialContext(dialCtx, "tcp", endpoint.Address)
	if err != nil {
		return c.failed(result, result.CheckedAt, fmt.Errorf("TCP connection failed: %w", err))
	}
	_ = connection.Close()
	result.State = domain.StateHealthy
	result.Message = "TCP connection succeeded"
	return result
}

func (c *Checker) checkTLS(ctx context.Context, endpoint domain.Endpoint, result domain.CheckResult, host string) domain.CheckResult {
	dialCtx, cancel := context.WithTimeout(ctx, endpoint.Timeout)
	defer cancel()
	tlsConfig := cloneTLSConfig(c.TLSConfig, host)
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: endpoint.Timeout},
		Config:    tlsConfig,
	}
	connection, err := dialer.DialContext(dialCtx, "tcp", endpoint.Address)
	if err != nil {
		valid := false
		result.TLSValid = &valid
		return c.failed(result, result.CheckedAt, fmt.Errorf("TLS validation failed: %w", err))
	}
	defer connection.Close()
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return c.failed(result, result.CheckedAt, errors.New("connection did not expose TLS state"))
	}
	state := tlsConnection.ConnectionState()
	applyTLS(&result, &state, endpoint.TLSWarnBefore, c.Now())
	if result.State == domain.StateUnknown {
		result.State = domain.StateHealthy
		result.Message = "TLS handshake and validation succeeded"
	}
	return result
}

func applyTLS(result *domain.CheckResult, state *tls.ConnectionState, warnBefore time.Duration, now time.Time) {
	valid := len(state.VerifiedChains) > 0
	result.TLSValid = &valid
	if !valid || len(state.PeerCertificates) == 0 {
		result.State = domain.StateUnavailable
		result.Error = "TLS certificate chain was not verified"
		result.Message = "TLS validation failed"
		return
	}
	expires := state.PeerCertificates[0].NotAfter.UTC()
	days := int(time.Until(expires).Hours() / 24)
	if !now.IsZero() {
		days = int(expires.Sub(now).Hours() / 24)
	}
	result.TLSExpiresAt = &expires
	result.CertificateDays = &days
	if !expires.After(now) {
		result.State = domain.StateUnavailable
		result.Error = "TLS certificate is expired"
		result.Message = "TLS certificate is expired"
	} else if warnBefore > 0 && expires.Before(now.Add(warnBefore)) {
		result.State = domain.StateDegraded
		result.Message = fmt.Sprintf("TLS certificate expires in %d days", days)
	}
}

func (c *Checker) failed(result domain.CheckResult, started time.Time, err error) domain.CheckResult {
	result.State = domain.StateUnavailable
	result.Error = err.Error()
	result.Message = err.Error()
	result.Latency = c.Now().Sub(started)
	return result
}

func endpointHost(endpoint domain.Endpoint) (string, error) {
	if endpoint.Type == domain.CheckHTTP {
		parsed, err := url.Parse(endpoint.Address)
		if err != nil {
			return "", err
		}
		if parsed.Hostname() == "" {
			return "", errors.New("URL host is empty")
		}
		return parsed.Hostname(), nil
	}
	host, _, err := net.SplitHostPort(endpoint.Address)
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", errors.New("host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	return strings.Trim(host, "[]"), nil
}

func SafeAddress(endpoint domain.Endpoint) string {
	if endpoint.Type != domain.CheckHTTP {
		return endpoint.Address
	}
	parsed, err := url.Parse(endpoint.Address)
	if err != nil {
		return endpoint.Address
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func statusText(code int) string {
	if text := http.StatusText(code); text != "" {
		return strconv.Itoa(code) + " " + text
	}
	return strconv.Itoa(code)
}

func cloneTLSConfig(source *tls.Config, serverName string) *tls.Config {
	var result *tls.Config
	if source == nil {
		result = &tls.Config{}
	} else {
		result = source.Clone()
	}
	if result.MinVersion == 0 {
		result.MinVersion = tls.VersionTLS12
	}
	if serverName != "" {
		result.ServerName = serverName
	}
	return result
}
