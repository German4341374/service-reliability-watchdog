package domain

import "time"

type State string

const (
	StateHealthy     State = "Healthy"
	StateDegraded    State = "Degraded"
	StateUnavailable State = "Unavailable"
	StateMaintenance State = "Maintenance"
	StateUnknown     State = "Unknown"
)

type CheckType string

const (
	CheckHTTP CheckType = "http"
	CheckTCP  CheckType = "tcp"
	CheckTLS  CheckType = "tls"
)

type SLOConfig struct {
	AvailabilityTarget float64       `json:"availabilityTarget"`
	LatencyTarget      time.Duration `json:"latencyTarget"`
	Window             time.Duration `json:"window"`
}

type Endpoint struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           CheckType         `json:"type"`
	Address        string            `json:"address"`
	Method         string            `json:"method,omitempty"`
	ExpectedStatus int               `json:"expectedStatus,omitempty"`
	ExpectedText   string            `json:"-"`
	Headers        map[string]string `json:"-"`
	Timeout        time.Duration     `json:"timeout"`
	Interval       time.Duration     `json:"interval"`
	Retries        int               `json:"retries"`
	LatencyTarget  time.Duration     `json:"latencyTarget"`
	TLSWarnBefore  time.Duration     `json:"tlsWarnBefore"`
	SLO            SLOConfig         `json:"slo"`
}

type CheckResult struct {
	ID                 int64         `json:"id,omitempty"`
	EndpointID         string        `json:"endpointId"`
	CheckedAt          time.Time     `json:"checkedAt"`
	State              State         `json:"state"`
	Latency            time.Duration `json:"latency"`
	HTTPStatus         int           `json:"httpStatus,omitempty"`
	DNSResolved        bool          `json:"dnsResolved"`
	TLSValid           *bool         `json:"tlsValid,omitempty"`
	TLSExpiresAt       *time.Time    `json:"tlsExpiresAt,omitempty"`
	CertificateDays    *int          `json:"certificateDaysRemaining,omitempty"`
	AttemptCount       int           `json:"attemptCount"`
	CircuitBreakerOpen bool          `json:"circuitBreakerOpen"`
	Message            string        `json:"message"`
	Error              string        `json:"error,omitempty"`
}

type Transition struct {
	ID         int64     `json:"id"`
	EndpointID string    `json:"endpointId"`
	FromState  State     `json:"fromState"`
	ToState    State     `json:"toState"`
	ChangedAt  time.Time `json:"changedAt"`
	Reason     string    `json:"reason"`
}

type Alert struct {
	ID         int64     `json:"id"`
	EndpointID string    `json:"endpointId"`
	State      State     `json:"state"`
	DedupeKey  string    `json:"dedupeKey"`
	CreatedAt  time.Time `json:"createdAt"`
	Message    string    `json:"message"`
}

type SLOStatus struct {
	Window             time.Duration `json:"window"`
	AvailabilityTarget float64       `json:"availabilityTarget"`
	Availability       float64       `json:"availability"`
	LatencyTarget      time.Duration `json:"latencyTarget"`
	LatencyCompliance  float64       `json:"latencyCompliance"`
	ErrorBudgetTotal   float64       `json:"errorBudgetTotal"`
	ErrorBudgetUsed    float64       `json:"errorBudgetUsed"`
	ErrorBudgetRemain  float64       `json:"errorBudgetRemaining"`
	BurnRate           float64       `json:"burnRate"`
	SampleCount        int64         `json:"sampleCount"`
}

type EndpointStatus struct {
	Endpoint    Endpoint     `json:"endpoint"`
	LastResult  *CheckResult `json:"lastResult,omitempty"`
	Uptime      float64      `json:"uptime"`
	SLO         SLOStatus    `json:"slo"`
	Transitions []Transition `json:"recentTransitions"`
}

type Aggregate struct {
	Total       int `json:"total"`
	Healthy     int `json:"healthy"`
	Degraded    int `json:"degraded"`
	Unavailable int `json:"unavailable"`
	Maintenance int `json:"maintenance"`
	Unknown     int `json:"unknown"`
}

type StatusSnapshot struct {
	GeneratedAt time.Time        `json:"generatedAt"`
	Aggregate   Aggregate        `json:"aggregate"`
	Endpoints   []EndpointStatus `json:"endpoints"`
}

func CalculateSLO(endpoint Endpoint, results []CheckResult, now time.Time) SLOStatus {
	window := endpoint.SLO.Window
	if window <= 0 {
		window = 30 * 24 * time.Hour
	}
	availabilityTarget := endpoint.SLO.AvailabilityTarget
	if availabilityTarget <= 0 {
		availabilityTarget = 0.999
	}
	latencyTarget := endpoint.SLO.LatencyTarget
	if latencyTarget <= 0 {
		latencyTarget = endpoint.LatencyTarget
	}

	cutoff := now.Add(-window)
	var eligible, good, latencyGood int64
	for _, result := range results {
		if result.CheckedAt.Before(cutoff) || result.State == StateMaintenance {
			continue
		}
		eligible++
		if result.State == StateHealthy || result.State == StateDegraded {
			good++
		}
		if latencyTarget > 0 && result.Latency <= latencyTarget && result.State != StateUnavailable {
			latencyGood++
		}
	}

	availability := ratio(good, eligible)
	latencyCompliance := ratio(latencyGood, eligible)
	allowedBad := 1 - availabilityTarget
	actualBad := 1 - availability
	burnRate := 0.0
	budgetUsed := 0.0
	budgetRemaining := 1.0
	if eligible > 0 && allowedBad > 0 {
		burnRate = actualBad / allowedBad
		budgetUsed = burnRate
		budgetRemaining = max(0, 1-budgetUsed)
	}

	return SLOStatus{
		Window:             window,
		AvailabilityTarget: availabilityTarget,
		Availability:       availability,
		LatencyTarget:      latencyTarget,
		LatencyCompliance:  latencyCompliance,
		ErrorBudgetTotal:   allowedBad,
		ErrorBudgetUsed:    budgetUsed,
		ErrorBudgetRemain:  budgetRemaining,
		BurnRate:           burnRate,
		SampleCount:        eligible,
	}
}

func CalculateSLOCounts(endpoint Endpoint, eligible, good, latencyGood int64) SLOStatus {
	window := endpoint.SLO.Window
	if window <= 0 {
		window = 30 * 24 * time.Hour
	}
	availabilityTarget := endpoint.SLO.AvailabilityTarget
	if availabilityTarget <= 0 {
		availabilityTarget = 0.999
	}
	latencyTarget := endpoint.SLO.LatencyTarget
	if latencyTarget <= 0 {
		latencyTarget = endpoint.LatencyTarget
	}
	availability := ratio(good, eligible)
	latencyCompliance := ratio(latencyGood, eligible)
	allowedBad := 1 - availabilityTarget
	actualBad := 1 - availability
	burnRate := 0.0
	budgetUsed := 0.0
	budgetRemaining := 1.0
	if eligible > 0 && allowedBad > 0 {
		burnRate = actualBad / allowedBad
		budgetUsed = burnRate
		budgetRemaining = max(0, 1-budgetUsed)
	}
	return SLOStatus{
		Window: window, AvailabilityTarget: availabilityTarget, Availability: availability,
		LatencyTarget: latencyTarget, LatencyCompliance: latencyCompliance,
		ErrorBudgetTotal: allowedBad, ErrorBudgetUsed: budgetUsed,
		ErrorBudgetRemain: budgetRemaining, BurnRate: burnRate, SampleCount: eligible,
	}
}

func ratio(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
