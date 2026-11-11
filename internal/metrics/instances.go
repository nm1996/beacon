package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nm1996/beacon/internal/discovery"
)

// instancesTimeout bounds how long a scrape waits for the instance count, so
// a slow backend makes the gauge disappear for one scrape instead of
// hanging it.
const instancesTimeout = 5 * time.Second

// ListFunc lists instances; discovery.Service.Discover and
// discovery.Registry.List both fit.
type ListFunc func(ctx context.Context, name string) ([]discovery.Instance, error)

// WatchInstances reports beacon_instances, the number of registered
// instances per service name, by calling list on every scrape. There is no
// separate count to drift from what the registry holds.
func (metrics *Metrics) WatchInstances(list ListFunc) {
	metrics.registry.MustRegister(&instancesCollector{list: list})
}

// instancesDesc describes beacon_instances.
var instancesDesc = prometheus.NewDesc(
	prometheus.BuildFQName(namespace, "", "instances"),
	"Registered instances, by service name.",
	[]string{"service"}, nil,
)

// instancesCollector computes beacon_instances when scraped.
type instancesCollector struct {
	list ListFunc
}

// Describe sends the one metric this collector produces.
func (collector *instancesCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- instancesDesc
}

// Collect lists every instance and sends one gauge per service name. If
// listing fails, it reports an invalid metric, which Prometheus surfaces as
// a scrape error instead of a misleading zero.
func (collector *instancesCollector) Collect(metrics chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), instancesTimeout)
	defer cancel()

	instances, err := collector.list(ctx, "")
	if err != nil {
		metrics <- prometheus.NewInvalidMetric(instancesDesc, err)
		return
	}

	counts := make(map[string]int)
	for _, instance := range instances {
		counts[instance.Name]++
	}

	for service, count := range counts {
		metrics <- prometheus.MustNewConstMetric(instancesDesc, prometheus.GaugeValue, float64(count), service)
	}
}
