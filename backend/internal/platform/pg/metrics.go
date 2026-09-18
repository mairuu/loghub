package pg

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolCollector reports a pool's connections to Prometheus, read from the
// pool when it is scraped rather than counted as they change.
type PoolCollector struct {
	pool *pgxpool.Pool

	acquired, idle, total, max *prometheus.Desc
	acquires, emptyAcquires    *prometheus.Desc
	acquireSeconds, cancelled  *prometheus.Desc
}

var _ prometheus.Collector = (*PoolCollector)(nil)

func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("loghub_db_pool_"+name, help, nil, nil)
	}
	return &PoolCollector{
		pool:           pool,
		acquired:       desc("acquired_connections", "Database connections in use."),
		idle:           desc("idle_connections", "Database connections open and waiting to be used."),
		total:          desc("total_connections", "Database connections open or being opened."),
		max:            desc("max_connections", "The most database connections the pool will open."),
		acquires:       desc("acquires_total", "Connections taken from the pool."),
		emptyAcquires:  desc("empty_acquires_total", "Connections taken from the pool that had to wait for one, because none was idle."),
		acquireSeconds: desc("acquire_seconds_total", "Time spent taking connections from the pool, waits included."),
		cancelled:      desc("canceled_acquires_total", "Waits for a connection that were given up, usually by a request that ended first."),
	}
}

func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.acquires, c.emptyAcquires, c.acquireSeconds, c.cancelled} {
		ch <- d
	}
}

func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.max, float64(s.MaxConns()))
	counter(c.acquires, float64(s.AcquireCount()))
	counter(c.emptyAcquires, float64(s.EmptyAcquireCount()))
	counter(c.acquireSeconds, s.AcquireDuration().Seconds())
	counter(c.cancelled, float64(s.CanceledAcquireCount()))
}
