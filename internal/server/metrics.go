package server

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)


const (
	namespace = "perseus"
)

type metrics struct {
	registry         *prometheus.Registry
	requestsDuration *prometheus.HistogramVec
}

func newMetrics() *metrics {
	m := &metrics{}
	m.registry = prometheus.NewRegistry()
	options := prometheus.ProcessCollectorOpts{
		Namespace: namespace,
	}
	m.registry.MustRegister(
		prometheus.NewProcessCollector(options),
		prometheus.NewGoCollector(),
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