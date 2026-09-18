package jobs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Outcomes of a run, as the metrics label them. A run stopped by shutdown
// isn't counted.
const (
	outcomeOK       = "ok"
	outcomeFailed   = "failed"
	outcomePanicked = "panicked"
)

type metrics struct {
	runs        *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	lastSuccess *prometheus.GaugeVec
}

// newMetrics registers the jobs' metrics with reg, or with a registry of
// their own when reg is nil. Every outcome starts at zero, so a rate over a
// job's failures exists before the first one.
func newMetrics(reg prometheus.Registerer, jobs []Job) *metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	m := &metrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_job_runs_total",
			Help: "Background job runs, by job and outcome: ok, failed or panicked.",
		}, []string{"job", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "loghub_job_duration_seconds",
			Help:    "How long a background job's run took, by job.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"job"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "loghub_job_last_success_timestamp_seconds",
			Help: "When a background job last finished without an error, in Unix seconds.",
		}, []string{"job"}),
	}
	reg.MustRegister(m.runs, m.duration, m.lastSuccess)
	for _, job := range jobs {
		for _, outcome := range []string{outcomeOK, outcomeFailed, outcomePanicked} {
			m.runs.WithLabelValues(job.Name, outcome)
		}
	}
	return m
}

func (m *metrics) finished(job, outcome string, took time.Duration) {
	m.runs.WithLabelValues(job, outcome).Inc()
	m.duration.WithLabelValues(job).Observe(took.Seconds())
	if outcome == outcomeOK {
		m.lastSuccess.WithLabelValues(job).SetToCurrentTime()
	}
}
