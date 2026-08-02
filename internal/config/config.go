package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

type Config struct {
	Server      ServerConfig        `yaml:"server"`
	Database    DatabaseConfig      `yaml:"database"`
	Scheduler   SchedulerConfig     `yaml:"scheduler"`
	Alerting    AlertingConfig      `yaml:"alerting"`
	Endpoints   []EndpointConfig    `yaml:"endpoints"`
	Maintenance []MaintenanceWindow `yaml:"maintenanceWindows"`
}

type ServerConfig struct {
	Address         string   `yaml:"address"`
	ShutdownTimeout Duration `yaml:"shutdownTimeout"`
}

type DatabaseConfig struct {
	URL            string   `yaml:"url"`
	ConnectTimeout Duration `yaml:"connectTimeout"`
	MaxConnections int32    `yaml:"maxConnections"`
}

type SchedulerConfig struct {
	Workers          int      `yaml:"workers"`
	ConcurrencyLimit int      `yaml:"concurrencyLimit"`
	QueueSize        int      `yaml:"queueSize"`
	RetryBaseDelay   Duration `yaml:"retryBaseDelay"`
	RetryMaxDelay    Duration `yaml:"retryMaxDelay"`
	CircuitFailures  int      `yaml:"circuitFailures"`
	CircuitCooldown  Duration `yaml:"circuitCooldown"`
}

type AlertingConfig struct {
	DedupeWindow Duration `yaml:"dedupeWindow"`
}

type SLOConfig struct {
	AvailabilityTarget float64  `yaml:"availabilityTarget"`
	LatencyTarget      Duration `yaml:"latencyTarget"`
	Window             Duration `yaml:"window"`
}

type EndpointConfig struct {
	ID             string            `yaml:"id"`
	Name           string            `yaml:"name"`
	Type           string            `yaml:"type"`
	Address        string            `yaml:"address"`
	Method         string            `yaml:"method"`
	ExpectedStatus int               `yaml:"expectedStatus"`
	ExpectedText   string            `yaml:"expectedText"`
	Headers        map[string]string `yaml:"headers"`
	Timeout        Duration          `yaml:"timeout"`
	Interval       Duration          `yaml:"interval"`
	Retries        int               `yaml:"retries"`
	LatencyTarget  Duration          `yaml:"latencyTarget"`
	TLSWarnBefore  Duration          `yaml:"tlsWarnBefore"`
	SLO            SLOConfig         `yaml:"slo"`
}

type MaintenanceWindow struct {
	Name      string    `yaml:"name" json:"name"`
	Start     time.Time `yaml:"start" json:"start"`
	End       time.Time `yaml:"end" json:"end"`
	Endpoints []string  `yaml:"endpoints" json:"endpoints"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(os.ExpandEnv(string(data))))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Database.URL == "" {
		return errors.New("database.url is required")
	}
	if c.Scheduler.Workers < 1 || c.Scheduler.ConcurrencyLimit < 1 {
		return errors.New("scheduler workers and concurrencyLimit must be positive")
	}
	if len(c.Endpoints) == 0 {
		return errors.New("at least one endpoint is required")
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for index, endpoint := range c.Endpoints {
		if err := endpoint.Validate(); err != nil {
			return fmt.Errorf("endpoints[%d]: %w", index, err)
		}
		if _, exists := seen[endpoint.ID]; exists {
			return fmt.Errorf("duplicate endpoint id %q", endpoint.ID)
		}
		seen[endpoint.ID] = struct{}{}
	}
	for index, window := range c.Maintenance {
		if window.Name == "" || !window.End.After(window.Start) {
			return fmt.Errorf("maintenanceWindows[%d] must have a name and end after start", index)
		}
		for _, endpointID := range window.Endpoints {
			if _, exists := seen[endpointID]; !exists {
				return fmt.Errorf("maintenance window %q references unknown endpoint %q", window.Name, endpointID)
			}
		}
	}
	return nil
}

func (e EndpointConfig) Validate() error {
	if e.ID == "" || e.Name == "" || e.Address == "" {
		return errors.New("id, name, and address are required")
	}
	if !slices.Contains([]string{"http", "tcp", "tls"}, e.Type) {
		return fmt.Errorf("unsupported type %q", e.Type)
	}
	if e.Timeout.Duration <= 0 || e.Interval.Duration <= 0 {
		return errors.New("timeout and interval must be positive")
	}
	if e.Retries < 0 || e.Retries > 10 {
		return errors.New("retries must be between 0 and 10")
	}
	if e.SLO.AvailabilityTarget <= 0 || e.SLO.AvailabilityTarget >= 1 {
		return errors.New("slo.availabilityTarget must be between 0 and 1")
	}
	if e.Type == "http" {
		parsed, err := url.Parse(e.Address)
		if err != nil || !slices.Contains([]string{"http", "https"}, parsed.Scheme) || parsed.Host == "" {
			return errors.New("HTTP address must be an absolute http or https URL")
		}
	} else if _, _, err := net.SplitHostPort(e.Address); err != nil {
		return fmt.Errorf("TCP/TLS address must use host:port: %w", err)
	}
	return nil
}

func (c Config) DomainEndpoints() []domain.Endpoint {
	result := make([]domain.Endpoint, 0, len(c.Endpoints))
	for _, endpoint := range c.Endpoints {
		result = append(result, domain.Endpoint{
			ID: endpoint.ID, Name: endpoint.Name, Type: domain.CheckType(endpoint.Type),
			Address: endpoint.Address, Method: endpoint.Method,
			ExpectedStatus: endpoint.ExpectedStatus, ExpectedText: endpoint.ExpectedText,
			Headers: endpoint.Headers, Timeout: endpoint.Timeout.Duration,
			Interval: endpoint.Interval.Duration, Retries: endpoint.Retries,
			LatencyTarget: endpoint.LatencyTarget.Duration,
			TLSWarnBefore: endpoint.TLSWarnBefore.Duration,
			SLO: domain.SLOConfig{
				AvailabilityTarget: endpoint.SLO.AvailabilityTarget,
				LatencyTarget:      endpoint.SLO.LatencyTarget.Duration,
				Window:             endpoint.SLO.Window.Duration,
			},
		})
	}
	return result
}

func (c Config) InMaintenance(endpointID string, now time.Time) bool {
	for _, window := range c.Maintenance {
		if now.Before(window.Start) || !now.Before(window.End) {
			continue
		}
		if len(window.Endpoints) == 0 || slices.Contains(window.Endpoints, endpointID) {
			return true
		}
	}
	return false
}

func applyDefaults(c *Config) {
	if c.Server.Address == "" {
		c.Server.Address = ":8080"
	}
	if c.Server.ShutdownTimeout.Duration == 0 {
		c.Server.ShutdownTimeout.Duration = 10 * time.Second
	}
	if c.Database.ConnectTimeout.Duration == 0 {
		c.Database.ConnectTimeout.Duration = 10 * time.Second
	}
	if c.Database.MaxConnections == 0 {
		c.Database.MaxConnections = 10
	}
	if c.Scheduler.Workers == 0 {
		c.Scheduler.Workers = 4
	}
	if c.Scheduler.ConcurrencyLimit == 0 {
		c.Scheduler.ConcurrencyLimit = c.Scheduler.Workers
	}
	if c.Scheduler.QueueSize == 0 {
		c.Scheduler.QueueSize = 100
	}
	if c.Scheduler.RetryBaseDelay.Duration == 0 {
		c.Scheduler.RetryBaseDelay.Duration = 200 * time.Millisecond
	}
	if c.Scheduler.RetryMaxDelay.Duration == 0 {
		c.Scheduler.RetryMaxDelay.Duration = 5 * time.Second
	}
	if c.Scheduler.CircuitFailures == 0 {
		c.Scheduler.CircuitFailures = 3
	}
	if c.Scheduler.CircuitCooldown.Duration == 0 {
		c.Scheduler.CircuitCooldown.Duration = 30 * time.Second
	}
	if c.Alerting.DedupeWindow.Duration == 0 {
		c.Alerting.DedupeWindow.Duration = 15 * time.Minute
	}
	for index := range c.Endpoints {
		e := &c.Endpoints[index]
		if e.Method == "" {
			e.Method = "GET"
		}
		if e.ExpectedStatus == 0 && e.Type == "http" {
			e.ExpectedStatus = 200
		}
		if e.Timeout.Duration == 0 {
			e.Timeout.Duration = 5 * time.Second
		}
		if e.Interval.Duration == 0 {
			e.Interval.Duration = 30 * time.Second
		}
		if e.LatencyTarget.Duration == 0 {
			e.LatencyTarget.Duration = 1 * time.Second
		}
		if e.TLSWarnBefore.Duration == 0 {
			e.TLSWarnBefore.Duration = 30 * 24 * time.Hour
		}
		if e.SLO.AvailabilityTarget == 0 {
			e.SLO.AvailabilityTarget = 0.999
		}
		if e.SLO.LatencyTarget.Duration == 0 {
			e.SLO.LatencyTarget.Duration = e.LatencyTarget.Duration
		}
		if e.SLO.Window.Duration == 0 {
			e.SLO.Window.Duration = 30 * 24 * time.Hour
		}
	}
}
