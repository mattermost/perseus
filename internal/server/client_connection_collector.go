package server

import "github.com/prometheus/client_golang/prometheus"

func (s *Server) addServerMetricsDescriptions() {
	fqName := func(name string) string {
		return serviceName + "_" + name
	}

	clientConnectionsMetricDesc := prometheus.NewDesc(
		fqName("num_client_connections_to_perseus_server"),
		"Number of clients connected to the perseus server",
		nil, prometheus.Labels{},
	)

	s.numConnectedClientsPrometheusDesc = clientConnectionsMetricDesc

}

// Describe implements prometheus Collector interface on the Server struct
func (s *Server) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.numConnectedClientsPrometheusDesc
}

// Collect implements prometheus Collector interface on the Server struct
func (s *Server) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(s.numConnectedClientsPrometheusDesc, prometheus.GaugeValue, float64(s.numConnectedClients))
}
