package discovery_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

func TestServiceNames(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{name: "orders", valid: true},
		{name: "payment-service", valid: true},
		{name: "orders.v2", valid: true},
		{name: "billing_api", valid: true},
		{name: "a", valid: true},
		{name: "0", valid: true},
		{name: "3d-render", valid: true},
		{name: strings.Repeat("a", 63), valid: true},

		{name: ""},
		{name: strings.Repeat("a", 64)},
		{name: "Orders"},
		{name: "ORDERS"},
		{name: "orders "},
		{name: " orders"},
		{name: "or ders"},
		{name: "orders\t"},
		{name: "-orders"},
		{name: "orders-"},
		{name: ".orders"},
		{name: "orders."},
		{name: "_orders"},
		{name: "orders/v2"},
		{name: "orders:8080"},
		{name: "ordérs"},
		{name: "orders\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registration(tt.name)

			_, err := newService(t).Register(t.Context(), reg, anyone)

			if tt.valid && err != nil {
				t.Errorf("expected %q to be accepted, got %v", tt.name, err)
			}

			if !tt.valid && !errors.Is(err, discovery.ErrInvalidRegistration) {
				t.Errorf("expected %q to be rejected as invalid, got %v", tt.name, err)
			}
		})
	}
}

func TestAddresses(t *testing.T) {
	tests := []struct {
		address string
		valid   bool
	}{
		{address: "10.0.0.12", valid: true},
		{address: "::1", valid: true},
		{address: "2001:db8::42", valid: true},
		{address: "fe80::1%eth0", valid: true},
		{address: "localhost", valid: true},
		{address: "orders.internal", valid: true},
		{address: "orders.default.svc.cluster.local", valid: true},
		{address: "orders.internal.", valid: true},
		{address: "Orders-1.Example.COM", valid: true},
		{address: strings.Repeat("a", 63) + ".example", valid: true},

		{address: ""},
		{address: " 10.0.0.12"},
		{address: "10.0.0.12 "},
		{address: "http://orders.internal"},
		{address: "orders.internal:8080"},
		{address: "[::1]"},
		{address: "orders.internal/health"},
		{address: "orders internal"},
		{address: "-orders.internal"},
		{address: "orders-.internal"},
		{address: "orders..internal"},
		{address: "orders_internal"},
		{address: strings.Repeat("a", 64) + ".example"},
		{address: strings.Repeat("a.", 127) + "aa"},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			reg := registration("orders")
			reg.Address = tt.address

			_, err := newService(t).Register(t.Context(), reg, anyone)

			if tt.valid && err != nil {
				t.Errorf("expected %q to be accepted, got %v", tt.address, err)
			}

			if !tt.valid && !errors.Is(err, discovery.ErrInvalidRegistration) {
				t.Errorf("expected %q to be rejected as invalid, got %v", tt.address, err)
			}
		})
	}
}

func TestInvalidNameMessageExplainsTheRule(t *testing.T) {
	_, err := newService(t).Register(t.Context(), registration("Orders"), anyone)

	if err == nil || !strings.Contains(err.Error(), "lowercase") || !strings.Contains(err.Error(), "63") {
		t.Errorf("expected the error to state the naming rule, got %v", err)
	}
}

// serviceWithLimits returns a Service over an empty registry whose length
// limits are name and address.
func serviceWithLimits(t *testing.T, name, address int) *discovery.Service {
	t.Helper()

	config := testConfig
	config.MaxNameLength = name
	config.MaxAddressLength = address

	service, err := discovery.New(memory.New(), config, discard)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return service
}

func TestNameLengthIsConfigurable(t *testing.T) {
	service := serviceWithLimits(t, 10, 0)

	if _, err := service.Register(t.Context(), registration(strings.Repeat("a", 10)), anyone); err != nil {
		t.Errorf("expected a name at the limit to be accepted, got %v", err)
	}

	_, err := service.Register(t.Context(), registration(strings.Repeat("a", 11)), anyone)
	if !errors.Is(err, discovery.ErrInvalidRegistration) || !strings.Contains(err.Error(), "at most 10 characters") {
		t.Errorf("expected a name over the limit to be rejected with the limit in the message, got %v", err)
	}

	// A raised limit lets in names longer than the default.
	long := strings.Repeat("a", discovery.MaxLengthLimit)
	if _, err := serviceWithLimits(t, discovery.MaxLengthLimit, 0).Register(t.Context(), registration(long), anyone); err != nil {
		t.Errorf("expected a %d character name to be accepted with a raised limit, got %v", len(long), err)
	}
}

func TestAddressLengthIsConfigurable(t *testing.T) {
	service := serviceWithLimits(t, 0, 15)

	for _, tt := range []struct {
		address string
		valid   bool
	}{
		{address: "orders.internal", valid: true},
		{address: "orders.internal1"},
		// IP addresses are not held to the limit.
		{address: "2001:db8:85a3::8a2e:370:7334", valid: true},
	} {
		reg := registration("orders")
		reg.Address = tt.address

		_, err := service.Register(t.Context(), reg, anyone)

		if tt.valid && err != nil {
			t.Errorf("expected %q to be accepted, got %v", tt.address, err)
		}

		if !tt.valid && (!errors.Is(err, discovery.ErrInvalidRegistration) || !strings.Contains(err.Error(), "at most 15 characters")) {
			t.Errorf("expected %q to be rejected with the limit in the message, got %v", tt.address, err)
		}
	}
}

func TestLengthLimitsOutOfRangeAreRejected(t *testing.T) {
	for _, tt := range []struct {
		name    string
		config  func(*discovery.Config)
		wantErr string
	}{
		{name: "negative name", config: func(c *discovery.Config) { c.MaxNameLength = -1 }, wantErr: "max name length"},
		{name: "name over the limit", config: func(c *discovery.Config) { c.MaxNameLength = discovery.MaxLengthLimit + 1 }, wantErr: "max name length"},
		{name: "negative address", config: func(c *discovery.Config) { c.MaxAddressLength = -1 }, wantErr: "max address length"},
		{name: "address over the limit", config: func(c *discovery.Config) { c.MaxAddressLength = discovery.MaxLengthLimit + 1 }, wantErr: "max address length"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := testConfig
			tt.config(&config)

			if _, err := discovery.New(memory.New(), config, discard); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected an error about %q, got %v", tt.wantErr, err)
			}
		})
	}
}
