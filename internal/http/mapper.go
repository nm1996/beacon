package http

import "github.com/nm1996/beacon/internal/registry"

// MapRegisterRequestToService turns a registration request into the service
// the registry stores. It does not validate anything; the registry does.
func MapRegisterRequestToService(dto RegisterRequest) *registry.Service {
	return &registry.Service{
		Name:    dto.Name,
		Address: dto.Address,
		Port:    dto.Port,
	}
}

// MapServiceToResponse picks the fields of a registered service that are
// returned to API clients.
func MapServiceToResponse(service *registry.Service) Service {
	return Service{
		Name:    service.Name,
		Address: service.Address,
		Port:    service.Port,
	}
}
