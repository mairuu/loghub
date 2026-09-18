package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Sign-in outcomes, as the metrics label them. Only attempts that reach the
// password check are counted.
const (
	signInOK      = "signed_in"
	signInRefused = "invalid_credentials"
	signInLimited = "too_many_attempts"
)

// metrics is what the API counts for Prometheus. Every label has a closed
// set of values: a route is a registered pattern, a tenant exists, a source
// is one the normalizer knows, and a code is one of the API's.
type metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	ingested *prometheus.CounterVec
	rejected *prometheus.CounterVec
	signIns  *prometheus.CounterVec
}

// newMetrics registers the API's metrics with reg, or with a registry of
// their own when reg is nil.
func newMetrics(reg prometheus.Registerer) *metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_http_requests_total",
			Help: "API requests answered, by method, route and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "loghub_http_request_duration_seconds",
			Help:    "How long the API took to answer a request, by method and route.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"method", "route"}),
		ingested: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_ingested_events_total",
			Help: "Events stored, by tenant and source.",
		}, []string{"tenant", "source"}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_rejected_records_total",
			Help: "Ingested records rejected, by error code.",
		}, []string{"code"}),
		signIns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_sign_ins_total",
			Help: "Sign-in attempts that reached the password check, by outcome: signed_in, invalid_credentials or too_many_attempts.",
		}, []string{"outcome"}),
	}
	reg.MustRegister(m.requests, m.duration, m.ingested, m.rejected, m.signIns)
	for _, outcome := range []string{signInOK, signInRefused, signInLimited} {
		m.signIns.WithLabelValues(outcome)
	}
	return m
}

// request counts an answered request. route is the pattern it matched.
func (m *metrics) request(method, route string, status int, took time.Duration) {
	method, route = requestLabels(method, route)
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(method, route).Observe(took.Seconds())
}

// requestLabels keeps the method and route labels to a closed set. A route
// is a ServeMux pattern such as "POST /api/v1/auth/login", and a request
// that matched none is counted under "unmatched". Its method counts only if
// the pattern named one, or if it is a standard one, since a client may send
// any word as a method.
func requestLabels(method, pattern string) (string, string) {
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return method, path
	}
	route := pattern
	if route == "" {
		route = "unmatched"
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
	default:
		method = "other"
	}
	return method, route
}
