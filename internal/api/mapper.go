package api

import "github.com/nm1996/beacon/internal/registry"

// mapRegisterRequest turns a registration request into the service
// the registry stores. It does not validate anything; the registry does.
func mapRegisterRequest(dto RegisterRequest) *registry.Service {
	return &registry.Service{
		Name:    dto.Name,
		Address: dto.Address,
		Port:    dto.Port,
	}
}

// mapServiceResponse picks the fields of a registered service that are
// returned to API clients.
func mapServiceResponse(service *registry.Service) Service {
	return Service{
		ID:            service.ID,
		Name:          service.Name,
		Address:       service.Address,
		Port:          service.Port,
		LastHeartbeat: service.LastHeartbeat.UTC(),
	}
}
