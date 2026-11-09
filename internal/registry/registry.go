package registry

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type ServiceDiscoveryRegistrator interface {
	Register(service *Service) (string, error)
	Heartbeat(serviceID string) error
	GetServices(name string) []*Service
	Unregister(serviceID string) error
	Cleanup(timeout time.Duration) int
}

type Registry struct {
	mu       sync.RWMutex
	services map[string]*Service
}

var _ ServiceDiscoveryRegistrator = (*Registry)(nil)

func NewRegistry() *Registry {
	return &Registry{
		services: make(map[string]*Service),
	}
}

// Register validates service and stores a copy under a newly generated ID.
// It returns an error wrapping ErrInvalidService when validation fails.
func (r *Registry) Register(service *Service) (string, error) {
	if err := service.validate(); err != nil {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	serviceCopy := *service
	serviceCopy.ID = uuid.New().String()
	serviceCopy.RegisteredAt = now
	serviceCopy.LastHeartbeat = now

	r.services[serviceCopy.ID] = &serviceCopy

	return serviceCopy.ID, nil
}

// Heartbeat refreshes the last heartbeat time of the service with the given ID.
// It returns ErrServiceNotFound when no such service is registered.
func (r *Registry) Heartbeat(serviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	service, exists := r.services[serviceID]
	if !exists {
		return ErrServiceNotFound
	}

	service.LastHeartbeat = time.Now()

	return nil
}

func (r *Registry) GetServices(name string) []*Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Service, 0, len(r.services))

	for _, service := range r.services {
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
func (r *Registry) Unregister(serviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[serviceID]; !exists {
		return ErrServiceNotFound
	}

	delete(r.services, serviceID)

	return nil
}

func (r *Registry) Cleanup(timeout time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	count := 0

	for id, service := range r.services {
		if now.Sub(service.LastHeartbeat) > timeout {
			delete(r.services, id)
			count++
		}
	}

	return count
}
