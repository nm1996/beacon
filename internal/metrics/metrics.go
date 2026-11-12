// Package metrics exposes Beacon's Prometheus metrics.
//
// Nothing else in Beacon depends on Prometheus. This package observes the
// other layers from the outside: InstrumentHTTP wraps the HTTP handler,
// InstrumentRegistry wraps any discovery.Registry backend, and WatchInstances
// counts registered instances when Prometheus scrapes. bootstrap decides
// whether to use any of it.
package metrics

import (
	"net/http"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// namespace prefixes every Beacon metric.
const namespace = "beacon"

// Metrics owns one Prometheus registry and the collectors registered in it.
// It never touches the global default registry, so separate instances, such
// as one per test, do not interfere.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	registryOperations *prometheus.CounterVec
	registryDuration   *prometheus.HistogramVec
	expiredInstances   prometheus.Counter

	authFailures *prometheus.CounterVec
}

// New returns Metrics with the Go runtime and process collectors and a
// beacon_build_info gauge reporting version.
func New(version string) *Metrics {
	registry := prometheus.NewRegistry()

	metrics := &Metrics{
		registry: registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "HTTP requests served, by method, route pattern and status code.",
		}, []string{"method", "route", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "Time spent serving HTTP requests, by method and route pattern.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route"}),
		registryOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "registry_operations_total",
			Help:      "Calls to the storage backend, by operation and result (ok, not_found or error).",
		}, []string{"operation", "result"}),
		registryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "registry_operation_duration_seconds",
			Help:      "Time spent in the storage backend, by operation.",
			Buckets:   []float64{.0001, .0005, .001, .005, .01, .05, .1, .5, 1},
		}, []string{"operation"}),
		expiredInstances: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "expired_instances_total",
			Help:      "Instances removed by cleanup because they stopped sending heartbeats.",
		}),
		authFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "auth_failures_total",
			Help:      "Requests refused by authentication, by reason (missing, invalid or forbidden).",
		}, []string{"reason"}),
	}

	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   namespace,
		Name:        "build_info",
		Help:        "Always 1; the labels describe the running build.",
		ConstLabels: prometheus.Labels{"version": version, "goversion": runtime.Version()},
	})
	buildInfo.Set(1)

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.registryOperations,
		metrics.registryDuration,
		metrics.expiredInstances,
		metrics.authFailures,
	)

	return metrics
}

// AuthFailed counts one request refused by authentication for reason.
func (metrics *Metrics) AuthFailed(reason string) {
	metrics.authFailures.WithLabelValues(reason).Inc()
}

// Handler serves every metric in the Prometheus text format.
func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{Registry: metrics.registry})
}
