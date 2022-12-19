package server

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	registry *prometheus.Registry
}

func newMetrics() *metrics {
	m := &metrics{}
	m.registry = prometheus.NewRegistry()
	options := collectors.ProcessCollectorOpts{
		Namespace: serviceName,
	}
	m.registry.MustRegister(
		collectors.NewProcessCollector(options),
		collectors.NewGoCollector(),
	)

	return m
}

// metricsHandler returns the handler that is going to be used by the
// service to expose the metrics.
func (m *metrics) metricsHandler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		Timeout:           30 * time.Second,
		EnableOpenMetrics: true,
	})
}

func (m *metrics) registerCollector(collector prometheus.Collector) {
	m.registry.MustRegister(collector)
}
