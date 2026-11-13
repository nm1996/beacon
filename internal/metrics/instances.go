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

// ListFunc lists instances; discovery.Service.List and
// discovery.Registry.List both fit.
type ListFunc func(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error)

// WatchInstances reports beacon_instances, the number of registered
// instances, and beacon_services, the number of distinct service names, by
// calling list on every scrape. There is no separate count to drift from
// what the registry holds.
//
// Neither carries a service label. Service names come from clients, so a
// per-name label would let any client create a new time series per name
// it registers; the API reports per-service counts instead.
func (metrics *Metrics) WatchInstances(list ListFunc) {
	metrics.registry.MustRegister(&instancesCollector{list: list})
}

var (
	// instancesDesc describes beacon_instances.
	instancesDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "instances"),
		"Registered instances.",
		nil, nil,
	)

	// servicesDesc describes beacon_services.
	servicesDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "services"),
		"Distinct service names with at least one registered instance.",
		nil, nil,
	)
)

// instancesCollector computes beacon_instances and beacon_services when
// scraped.
type instancesCollector struct {
	list ListFunc
}

// Describe sends the metrics this collector produces.
func (collector *instancesCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- instancesDesc
	descs <- servicesDesc
}

// Collect lists every instance and sends both counts. If listing fails, it
// reports invalid metrics, which Prometheus surfaces as a scrape error
// instead of a misleading zero.
func (collector *instancesCollector) Collect(metrics chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), instancesTimeout)
	defer cancel()

	instances, err := collector.list(ctx, discovery.ListQuery{})
	if err != nil {
		metrics <- prometheus.NewInvalidMetric(instancesDesc, err)
		metrics <- prometheus.NewInvalidMetric(servicesDesc, err)

		return
	}

	names := make(map[string]struct{})
	for _, instance := range instances {
		names[instance.Name] = struct{}{}
	}

	metrics <- prometheus.MustNewConstMetric(instancesDesc, prometheus.GaugeValue, float64(len(instances)))
	metrics <- prometheus.MustNewConstMetric(servicesDesc, prometheus.GaugeValue, float64(len(names)))
}
