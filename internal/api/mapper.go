package api

import "github.com/nm1996/beacon/internal/discovery"

// mapRegisterRequest turns a registration request into the registration the
// discovery layer validates and stores.
func mapRegisterRequest(dto RegisterRequest) discovery.Registration {
	return discovery.Registration{
		Name:    dto.Name,
		Address: dto.Address,
		Port:    dto.Port,
	}
}

// mapInstanceResponse picks the fields of a registered instance that are
// returned to API clients.
func mapInstanceResponse(instance discovery.Instance) Service {
	return Service{
		ID:            instance.ID,
		Name:          instance.Name,
		Address:       instance.Address,
		Port:          instance.Port,
		LastHeartbeat: instance.LastHeartbeat.UTC(),
	}
}
