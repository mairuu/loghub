package alerting

import "github.com/prometheus/client_golang/prometheus"

type metrics struct {
	raised      *prometheus.CounterVec
	evaluations *prometheus.CounterVec
	deliveries  *prometheus.CounterVec
}

// newMetrics registers the evaluator's metrics with reg, or with a registry
// of their own when reg is nil. Every outcome starts at zero.
func newMetrics(reg prometheus.Registerer) *metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	m := &metrics{
		raised: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_alerts_raised_total",
			Help: "Alerts raised by alert rules, by tenant.",
		}, []string{"tenant"}),
		evaluations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_alert_rule_evaluations_total",
			Help: "Alert rule evaluations, by outcome: ok or failed.",
		}, []string{"outcome"}),
		deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loghub_webhook_deliveries_total",
			Help: "Alerts sent to a rule's webhook, by outcome: delivered or failed.",
		}, []string{"outcome"}),
	}
	reg.MustRegister(m.raised, m.evaluations, m.deliveries)
	m.evaluations.WithLabelValues("ok")
	m.evaluations.WithLabelValues("failed")
	m.deliveries.WithLabelValues("delivered")
	m.deliveries.WithLabelValues("failed")
	return m
}
