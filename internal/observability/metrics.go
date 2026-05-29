package observability

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// numericSegment matches path segments that look like IDs (numeric, UUIDs, hex strings).
var numericSegment = regexp.MustCompile(`/[0-9a-fA-F-]{4,}`)

// NormalizePath reduces high-cardinality URL paths to a bounded set of labels.
// e.g. "/api/users/12345/orders" → "/api/users/:id/orders"
// This prevents unbounded Prometheus time series from attacker-generated paths.
func NormalizePath(path string) string {
	if path == "" {
		return "/"
	}
	// Replace numeric/UUID-like segments with :id
	normalized := numericSegment.ReplaceAllString(path, "/:id")

	// Cap path depth to prevent very long paths from creating unique series
	segments := 0
	for i, ch := range normalized {
		if ch == '/' {
			segments++
		}
		if segments > 5 {
			return normalized[:i] + "/..."
		}
	}
	return normalized
}

type Metrics struct {
	requestsTotal   *prometheus.CounterVec
	rateLimitHits   *prometheus.CounterVec
	upstreamLatency *prometheus.HistogramVec
	breakerState    *prometheus.GaugeVec
}

func NewMetrics(registerer prometheus.Registerer) *Metrics {
	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total gateway requests by tenant, route and status",
		},
		[]string{"tenant", "route", "status_code"},
	)
	rateLimitHits := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_rate_limit_hits_total",
			Help: "Total rate limit hits by tenant",
		},
		[]string{"tenant"},
	)
	upstreamLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_upstream_latency_seconds",
			Help:    "Upstream latency by tenant and upstream",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tenant", "upstream"},
	)
	breakerState := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_circuit_breaker_state",
			Help: "Circuit breaker state by tenant and upstream (0=closed,1=open,2=half-open)",
		},
		[]string{"tenant", "upstream"},
	)

	registerer.MustRegister(requestsTotal, rateLimitHits, upstreamLatency, breakerState)

	return &Metrics{
		requestsTotal:   requestsTotal,
		rateLimitHits:   rateLimitHits,
		upstreamLatency: upstreamLatency,
		breakerState:    breakerState,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.Handler()
}

func (m *Metrics) ObserveRequest(tenant, route string, statusCode int) {
	code := strconv.Itoa(statusCode)
	m.requestsTotal.WithLabelValues(tenant, NormalizePath(route), code).Inc()
}

func (m *Metrics) IncRateLimit(tenant string) {
	m.rateLimitHits.WithLabelValues(tenant).Inc()
}

func (m *Metrics) ObserveUpstreamLatency(tenant, upstream string, seconds float64) {
	m.upstreamLatency.WithLabelValues(tenant, upstream).Observe(seconds)
}

func (m *Metrics) SetBreakerState(tenant, upstream string, state int) {
	m.breakerState.WithLabelValues(tenant, upstream).Set(float64(state))
}
