// Package registry keeps track of running service instances in memory.
//
// It knows nothing about HTTP; transports call into it and translate its
// errors. All state is lost when the process stops, so services are expected
// to register again after a restart.
package registry

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Registry is a concurrency-safe, in-memory store of service instances keyed
// by their generated ID. Use New to create one; the zero value is not
// ready to use.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*Service
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		services: make(map[string]*Service),
	}
}

// Register validates service and stores a copy under a newly generated ID.
// It returns an error wrapping ErrInvalidService when validation fails.
func (registry *Registry) Register(service *Service) (string, error) {
	if err := service.validate(); err != nil {
		return "", err
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	now := time.Now()

	serviceCopy := *service
	serviceCopy.ID = uuid.New().String()
	serviceCopy.RegisteredAt = now
	serviceCopy.LastHeartbeat = now

	registry.services[serviceCopy.ID] = &serviceCopy

	return serviceCopy.ID, nil
}

// Heartbeat refreshes the last heartbeat time of the service with the given ID.
// It returns ErrServiceNotFound when no such service is registered.
func (registry *Registry) Heartbeat(serviceID string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	service, exists := registry.services[serviceID]
	if !exists {
		return ErrServiceNotFound
	}

	service.LastHeartbeat = time.Now()

	return nil
}

// GetServices returns the instances registered under name, or every instance
// when name is empty. The result holds copies, so callers may change them
// freely, and its order is not defined.
func (registry *Registry) GetServices(name string) []*Service {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	result := make([]*Service, 0, len(registry.services))

	for _, service := range registry.services {
		if name != "" && service.Name != name {
			continue
		}

		serviceCopy := *service
		result = append(result, &serviceCopy)
	}

	return result
}

// Unregister removes the service with the given ID.
// It returns ErrServiceNotFound when no such service is registered.
func (registry *Registry) Unregister(serviceID string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if _, exists := registry.services[serviceID]; !exists {
		return ErrServiceNotFound
	}

	delete(registry.services, serviceID)

	return nil
}

// Cleanup removes every instance whose last heartbeat is older than timeout
// and reports how many were removed.
func (registry *Registry) Cleanup(timeout time.Duration) int {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	now := time.Now()
	count := 0

	for id, service := range registry.services {
		if now.Sub(service.LastHeartbeat) > timeout {
			delete(registry.services, id)
			count++
		}
	}

	return count
}
