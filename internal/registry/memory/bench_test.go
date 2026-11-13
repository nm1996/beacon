package memory_test

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// sizes are the registry sizes each benchmark runs at: from a small team to
// ten times the default registry.max_instances.
var sizes = []int{100, 1_000, 10_000, 100_000}

// services is how many distinct service names a filled registry spreads
// its instances over.
const services = 100

// unlimited lets every Create through.
var unlimited = discovery.Limits{Total: math.MaxInt}

// anyOwner lets every Touch and Delete through.
var anyOwner = discovery.OwnerCheck{Any: true}

// filled returns a registry holding size instances with random UUIDs, as
// Service generates them, spread over services names and owners, and their
// IDs.
func filled(b *testing.B, size int) (*memory.MemoryRegistry, []string) {
	b.Helper()

	registry := memory.New()
	ids := make([]string, size)
	now := time.Now()

	for index := range size {
		ids[index] = uuid.NewString()

		instance := discovery.Instance{
			ID:            ids[index],
			Name:          fmt.Sprintf("service-%d", index%services),
			Address:       "10.0.0.1",
			Port:          8080,
			Owner:         fmt.Sprintf("owner-%d", index%services),
			RegisteredAt:  now,
			LastHeartbeat: now,
		}

		if err := registry.Create(context.Background(), instance, unlimited); err != nil {
			b.Fatalf("fill: %v", err)
		}
	}

	return registry, ids
}

// eachSize runs benchmark as a sub-benchmark per size.
func eachSize(b *testing.B, benchmark func(b *testing.B, size int)) {
	for _, size := range sizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			benchmark(b, size)
		})
	}
}

// BenchmarkCreateDelete registers and removes one instance in a registry
// of a given size, so the size stays put. The per-owner limit is on, as
// with authentication.
func BenchmarkCreateDelete(b *testing.B) {
	eachSize(b, func(b *testing.B, size int) {
		registry, _ := filled(b, size)
		ctx := context.Background()
		limits := discovery.Limits{Total: math.MaxInt, PerOwner: math.MaxInt}
		instance := discovery.Instance{Name: "orders", Address: "10.0.0.1", Port: 8080, Owner: "owner-0"}

		b.ReportAllocs()

		for b.Loop() {
			instance.ID = uuid.NewString()

			if err := registry.Create(ctx, instance, limits); err != nil {
				b.Fatal(err)
			}

			if err := registry.Delete(ctx, instance.ID, anyOwner); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkTouch is one heartbeat.
func BenchmarkTouch(b *testing.B) {
	eachSize(b, func(b *testing.B, size int) {
		registry, ids := filled(b, size)
		ctx := context.Background()
		now := time.Now()

		b.ReportAllocs()

		index := 0
		for b.Loop() {
			if err := registry.Touch(ctx, ids[index%size], now, anyOwner); err != nil {
				b.Fatal(err)
			}

			index++
		}
	})
}

// BenchmarkList covers the three lookups clients make: every instance at
// once, as a client without paging does; the first page of 500, as the Go
// client does; and one service's instances.
func BenchmarkList(b *testing.B) {
	queries := []struct {
		name  string
		query discovery.ListQuery
	}{
		{name: "all", query: discovery.ListQuery{}},
		{name: "page=500", query: discovery.ListQuery{Limit: 500}},
		{name: "one-service", query: discovery.ListQuery{Name: "service-7"}},
	}

	for _, query := range queries {
		b.Run(query.name, func(b *testing.B) {
			eachSize(b, func(b *testing.B, size int) {
				registry, _ := filled(b, size)
				ctx := context.Background()

				b.ReportAllocs()

				for b.Loop() {
					if _, err := registry.List(ctx, query.query); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkDeleteStaleNothingExpired is what the cleanup worker costs on
// every interval when all instances are healthy: a scan that removes
// nothing.
func BenchmarkDeleteStaleNothingExpired(b *testing.B) {
	eachSize(b, func(b *testing.B, size int) {
		registry, _ := filled(b, size)
		ctx := context.Background()
		cutoff := time.Now().Add(-time.Hour)

		b.ReportAllocs()

		for b.Loop() {
			if _, err := registry.DeleteStale(ctx, cutoff); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkDeleteStaleEverythingExpired removes every instance at once, as
// after a network partition. Refilling is left out of the timing.
func BenchmarkDeleteStaleEverythingExpired(b *testing.B) {
	eachSize(b, func(b *testing.B, size int) {
		ctx := context.Background()
		cutoff := time.Now().Add(time.Hour)

		b.ReportAllocs()

		for b.Loop() {
			b.StopTimer()
			registry, _ := filled(b, size)
			b.StartTimer()

			removed, err := registry.DeleteStale(ctx, cutoff)
			if err != nil || removed != size {
				b.Fatalf("expected %d removed, got %d, %v", size, removed, err)
			}
		}
	})
}

// BenchmarkMixedParallel is traffic from many goroutines at once: mostly
// heartbeats, some lookups of one service, and a few registrations, while
// every operation shares one lock. Run it with -cpu to vary how many
// goroutines compete; mise run bench-parallel does.
func BenchmarkMixedParallel(b *testing.B) {
	eachSize(b, func(b *testing.B, size int) {
		registry, ids := filled(b, size)
		ctx := context.Background()

		b.ReportAllocs()
		b.ResetTimer()

		b.RunParallel(func(pb *testing.PB) {
			random := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			now := time.Now()
			instance := discovery.Instance{Name: "orders", Address: "10.0.0.1", Port: 8080}

			for pb.Next() {
				switch roll := random.IntN(100); {
				case roll < 90:
					_ = registry.Touch(ctx, ids[random.IntN(size)], now, anyOwner)
				case roll < 99:
					_, _ = registry.List(ctx, discovery.ListQuery{Name: "service-7"})
				default:
					instance.ID = uuid.NewString()
					_ = registry.Create(ctx, instance, unlimited)
					_ = registry.Delete(ctx, instance.ID, anyOwner)
				}
			}
		})
	})
}

// BenchmarkHeartbeatDuringLookups is one heartbeat while other goroutines
// look up one service as fast as they can: how long a heartbeat waits for
// the lock that lookups hold. It reports the 99th percentile as p99-ns
// besides the mean, since a few long waits are what can make instances
// expire.
func BenchmarkHeartbeatDuringLookups(b *testing.B) {
	for _, lookups := range []int{0, 1, 4} {
		b.Run(fmt.Sprintf("lookups=%d", lookups), func(b *testing.B) {
			eachSize(b, func(b *testing.B, size int) {
				registry, ids := filled(b, size)
				ctx := context.Background()

				var (
					group sync.WaitGroup
					stop  atomic.Bool
				)

				for range lookups {
					group.Go(func() {
						for !stop.Load() {
							_, _ = registry.List(ctx, discovery.ListQuery{Name: "service-7"})
						}
					})
				}

				waits := make([]time.Duration, 0, 1<<16)
				index := 0

				for b.Loop() {
					start := time.Now()

					if err := registry.Touch(ctx, ids[index%size], start, anyOwner); err != nil {
						b.Fatal(err)
					}

					waits = append(waits, time.Since(start))
					index++
				}

				stop.Store(true)
				group.Wait()

				slices.Sort(waits)
				b.ReportMetric(float64(waits[len(waits)*99/100]), "p99-ns")
			})
		})
	}
}
