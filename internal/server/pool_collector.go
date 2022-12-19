package server

import "github.com/prometheus/client_golang/prometheus"

type poolCollector struct {
	p *Pool

	maxOpenConnections *prometheus.Desc

	openConnections  *prometheus.Desc
	inUseConnections *prometheus.Desc
	idleConnections  *prometheus.Desc

	waitCount         *prometheus.Desc
	waitDurationSec   *prometheus.Desc
	maxIdleClosed     *prometheus.Desc
	maxIdleTimeClosed *prometheus.Desc
	maxLifetimeClosed *prometheus.Desc
}

func newPoolCollector(p *Pool, destHost, destDB string) *poolCollector {
	fqName := func(name string) string {
		return serviceName + "_" + name
	}
	return &poolCollector{
		p: p,
		maxOpenConnections: prometheus.NewDesc(
			fqName("max_open_connections"),
			"Maximum number of open connections to the database.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		openConnections: prometheus.NewDesc(
			fqName("open_connections"),
			"The number of established connections both in use and idle.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		inUseConnections: prometheus.NewDesc(
			fqName("in_use_connections"),
			"The number of connections currently in use.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		idleConnections: prometheus.NewDesc(
			fqName("idle_connections"),
			"The number of idle connections.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		waitCount: prometheus.NewDesc(
			fqName("wait_count_total"),
			"The total number of connections waited for.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		waitDurationSec: prometheus.NewDesc(
			fqName("wait_duration_seconds_total"),
			"The total time (in seconds) blocked waiting for a new connection.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		maxIdleClosed: prometheus.NewDesc(
			fqName("max_idle_closed_total"),
			"The total number of connections closed due to SetMaxIdleConns.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		maxIdleTimeClosed: prometheus.NewDesc(
			fqName("max_idle_time_closed_total"),
			"The total number of connections closed due to SetConnMaxIdleTime.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
		maxLifetimeClosed: prometheus.NewDesc(
			fqName("max_lifetime_closed_total"),
			"The total number of connections closed due to SetConnMaxLifetime.",
			nil, prometheus.Labels{"dest_host": destHost, "dest_db": destDB},
		),
	}
}

// Describe implements Collector.
func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.maxOpenConnections
	ch <- c.openConnections
	ch <- c.inUseConnections
	ch <- c.idleConnections
	ch <- c.waitCount
	ch <- c.waitDurationSec
	ch <- c.maxIdleClosed
	ch <- c.maxIdleTimeClosed
	ch <- c.maxLifetimeClosed
}

// Collect implements Collector.
func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.p.Stats()
	ch <- prometheus.MustNewConstMetric(c.maxOpenConnections, prometheus.GaugeValue, float64(stats.MaxOpenConnections))
	ch <- prometheus.MustNewConstMetric(c.openConnections, prometheus.GaugeValue, float64(stats.OpenConnections))
	ch <- prometheus.MustNewConstMetric(c.inUseConnections, prometheus.GaugeValue, float64(stats.InUse))
	ch <- prometheus.MustNewConstMetric(c.idleConnections, prometheus.GaugeValue, float64(stats.Idle))
	ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitDurationSec, prometheus.CounterValue, stats.WaitDuration.Seconds())
	ch <- prometheus.MustNewConstMetric(c.maxIdleClosed, prometheus.CounterValue, float64(stats.MaxIdleClosed))
	ch <- prometheus.MustNewConstMetric(c.maxIdleTimeClosed, prometheus.CounterValue, float64(stats.MaxIdleTimeClosed))
	ch <- prometheus.MustNewConstMetric(c.maxLifetimeClosed, prometheus.CounterValue, float64(stats.MaxLifetimeClosed))
}
