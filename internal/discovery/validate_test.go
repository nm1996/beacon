package discovery_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/discovery"
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
