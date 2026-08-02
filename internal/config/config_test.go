package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExpandsEnvironmentAndDefaults(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "postgres://watchdog:local@localhost/watchdog")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `
database:
  url: ${TEST_DATABASE_URL}
endpoints:
  - id: api
    name: API
    type: http
    address: http://localhost:8080/health
    slo:
      availabilityTarget: 0.99
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.URL != "postgres://watchdog:local@localhost/watchdog" {
		t.Fatal("environment was not expanded")
	}
	if cfg.Endpoints[0].Timeout.Duration != 5*time.Second || cfg.Scheduler.Workers != 4 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "database:\n  url: postgres://localhost/x\nunknown: true\nendpoints: []\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsDuplicateAndUnsafeAddress(t *testing.T) {
	base := EndpointConfig{ID: "same", Name: "API", Type: "http", Address: "file:///etc/passwd", Timeout: Duration{time.Second}, Interval: Duration{time.Second}, SLO: SLOConfig{AvailabilityTarget: .99}}
	cfg := Config{Database: DatabaseConfig{URL: "postgres://local"}, Scheduler: SchedulerConfig{Workers: 1, ConcurrencyLimit: 1}, Endpoints: []EndpointConfig{base, base}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "absolute http") {
		t.Fatalf("expected URL validation, got %v", err)
	}
	base.Address = "http://localhost"
	cfg.Endpoints = []EndpointConfig{base, base}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestMaintenanceWindow(t *testing.T) {
	now := time.Now().UTC()
	cfg := Config{Maintenance: []MaintenanceWindow{{Name: "change", Start: now.Add(-time.Minute), End: now.Add(time.Minute), Endpoints: []string{"api"}}}}
	if !cfg.InMaintenance("api", now) || cfg.InMaintenance("other", now) {
		t.Fatal("maintenance scope mismatch")
	}
}

func TestCommittedComposeConfigurationLoads(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://watchdog:local@postgres/watchdog")
	cfg, err := Load("../../config.compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Endpoints) != 3 || cfg.Scheduler.ConcurrencyLimit != 3 {
		t.Fatalf("unexpected committed configuration: %+v", cfg)
	}
}
