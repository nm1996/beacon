package memory

import (
	"testing"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/registrytest"
)

func TestRegistryContract(t *testing.T) {
	registrytest.Run(t, func(*testing.T) discovery.Registry {
		return New()
	})
}
