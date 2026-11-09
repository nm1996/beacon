package http

import "github.com/nm1996/beacon/internal/registry"

func MapRegisterRequestToService(dto RegisterRequest) *registry.Service {
	return &registry.Service{
		Name:    dto.Name,
		Address: dto.Address,
		Port:    dto.Port,
	}
}

func MapServiceToResponse(service *registry.Service) Service {
	return Service{
		Name:    service.Name,
		Address: service.Address,
		Port:    service.Port,
	}
}
