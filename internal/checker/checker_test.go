package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

type resolverFunc func(context.Context, string) ([]string, error)

func (f resolverFunc) LookupHost(ctx context.Context, host string) ([]string, error) {
	return f(ctx, host)
}

func TestHTTPCheckValidatesStatusAndText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte("service ready"))
	}))
	defer server.Close()
	result := New().Check(context.Background(), domain.Endpoint{
		ID: "api", Type: domain.CheckHTTP, Address: server.URL, Method: http.MethodGet,
		ExpectedStatus: http.StatusAccepted, ExpectedText: "ready", Timeout: time.Second,
		LatencyTarget: time.Second,
	})
	if result.State != domain.StateHealthy || result.HTTPStatus != http.StatusAccepted || !result.DNSResolved {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHTTPStatusAndTextFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("wrong")) }))
	defer server.Close()
	endpoint := domain.Endpoint{ID: "api", Type: domain.CheckHTTP, Address: server.URL, Method: "GET", ExpectedStatus: 201, Timeout: time.Second}
	result := New().Check(context.Background(), endpoint)
	if result.State != domain.StateUnavailable || !strings.Contains(result.Error, "expected HTTP") {
		t.Fatalf("unexpected status result: %+v", result)
	}
	endpoint.ExpectedStatus = 200
	endpoint.ExpectedText = "missing"
	result = New().Check(context.Background(), endpoint)
	if !strings.Contains(result.Error, "expected response text") {
		t.Fatalf("unexpected text result: %+v", result)
	}
}

func TestSlowHTTPIsDegraded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		writer.WriteHeader(200)
	}))
	defer server.Close()
	result := New().Check(context.Background(), domain.Endpoint{ID: "slow", Type: domain.CheckHTTP, Address: server.URL, Method: "GET", ExpectedStatus: 200, Timeout: time.Second, LatencyTarget: time.Millisecond})
	if result.State != domain.StateDegraded {
		t.Fatalf("expected degraded, got %+v", result)
	}
}

func TestDNSFailureStopsCheck(t *testing.T) {
	checker := New()
	checker.Resolver = resolverFunc(func(context.Context, string) ([]string, error) { return nil, errors.New("NXDOMAIN") })
	result := checker.Check(context.Background(), domain.Endpoint{ID: "dns", Type: domain.CheckTCP, Address: "missing.example:443", Timeout: time.Second})
	if result.DNSResolved || result.State != domain.StateUnavailable || !strings.Contains(result.Error, "DNS resolution") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestTCPCheckSuccessAndFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
	}()
	result := New().Check(context.Background(), domain.Endpoint{ID: "tcp", Type: domain.CheckTCP, Address: address, Timeout: time.Second, LatencyTarget: time.Second})
	_ = listener.Close()
	if result.State != domain.StateHealthy {
		t.Fatalf("expected healthy TCP, got %+v", result)
	}
	result = New().Check(context.Background(), domain.Endpoint{ID: "tcp", Type: domain.CheckTCP, Address: address, Timeout: 50 * time.Millisecond})
	if result.State != domain.StateUnavailable {
		t.Fatalf("expected unavailable TCP, got %+v", result)
	}
}

func TestTLSValidationAndExpirationMetadata(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	checker := New()
	checker.TLSConfig = &tls.Config{RootCAs: pool}
	result := checker.Check(context.Background(), domain.Endpoint{ID: "tls", Type: domain.CheckTLS, Address: parsed.Host, Timeout: time.Second, TLSWarnBefore: time.Hour, LatencyTarget: time.Second})
	if result.State != domain.StateHealthy || result.TLSValid == nil || !*result.TLSValid || result.TLSExpiresAt == nil {
		t.Fatalf("unexpected TLS result: %+v", result)
	}
}

func TestTLSValidationFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	result := New().Check(context.Background(), domain.Endpoint{ID: "tls", Type: domain.CheckTLS, Address: parsed.Host, Timeout: time.Second})
	if result.State != domain.StateUnavailable || result.TLSValid == nil || *result.TLSValid {
		t.Fatalf("expected invalid TLS, got %+v", result)
	}
}

func TestSafeAddressRemovesCredentialsAndQuery(t *testing.T) {
	endpoint := domain.Endpoint{Type: domain.CheckHTTP, Address: "https://user:secret@example.test/path?token=secret#part"}
	if got := SafeAddress(endpoint); got != "https://example.test/path" {
		t.Fatalf("unsafe address: %s", got)
	}
}
