package registry

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type ServiceDiscoveryRegistrator interface {
	Register(service *Service) string
	Heartbeat(serviceID string) bool
	GetServices(name string) []*Service
	Unregister(serviceID string) bool
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

func (r *Registry) Register(service *Service) string {
	if service == nil {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	serviceCopy := *service
	serviceCopy.ID = uuid.New().String()
	serviceCopy.RegisteredAt = now
	serviceCopy.LastHeartbeat = now

	r.services[serviceCopy.ID] = &serviceCopy

	return serviceCopy.ID
}

func (r *Registry) Heartbeat(serviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	service, exists := r.services[serviceID]
	if !exists {
		return false
	}

	service.LastHeartbeat = time.Now()

	return true
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

func (r *Registry) Unregister(serviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[serviceID]; !exists {
		return false
	}

	delete(r.services, serviceID)

	return true
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
