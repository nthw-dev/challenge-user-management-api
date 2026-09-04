package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// newMetrics builds the registry /metrics serves and the one gauge the worker feeds.
// The worker knows nothing of Prometheus; main hands it a closure that sets this gauge.
func newMetrics() (*prometheus.Registry, prometheus.Gauge) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	userGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "users_total",
		Help: "The latest user count the worker measured",
	})
	registry.MustRegister(userGauge)
	return registry, userGauge
}
