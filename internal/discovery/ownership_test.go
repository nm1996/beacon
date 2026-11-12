package discovery_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/registry/memory"
)

// anyone may change every instance, as callers could before ownership.
var anyone = discovery.Caller{AnyInstance: true}

var (
	orders   = discovery.Caller{Name: "orders"}
	payments = discovery.Caller{Name: "payments"}
	operator = discovery.Caller{Name: "operator", AnyInstance: true}
)

func TestRegisterRecordsTheCallerAsOwner(test *testing.T) {
	service := newService(test)

	instance, err := service.Register(test.Context(), registration("orders"), orders)
	if err != nil {
		test.Fatalf("register: %v", err)
	}

	if instance.Owner != "orders" {
		test.Errorf("expected owner %q, got %q", "orders", instance.Owner)
	}

	listed, err := service.Discover(test.Context(), "orders")
	if err != nil || len(listed) != 1 || listed[0].Owner != "orders" {
		test.Errorf("expected the stored instance to keep its owner, got %+v, %v", listed, err)
	}
}

func TestOnlyTheOwnerOrAManagerMayChangeAnInstance(test *testing.T) {
	cases := []struct {
		name    string
		owner   discovery.Caller
		caller  discovery.Caller
		wantErr error
	}{
		{name: "owner", owner: orders, caller: orders},
		{name: "another caller", owner: orders, caller: payments, wantErr: discovery.ErrInstanceNotOwned},
		{name: "nameless caller", owner: orders, caller: discovery.Caller{}, wantErr: discovery.ErrInstanceNotOwned},
		{name: "manager", owner: orders, caller: operator},
		{name: "unowned instance, any caller", owner: discovery.Caller{}, caller: payments},
		{name: "unowned instance, nameless caller", owner: discovery.Caller{}, caller: discovery.Caller{}},
		{name: "anyone, without authentication", owner: orders, caller: anyone},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			service := newService(test)

			instance, err := service.Register(test.Context(), registration("orders"), testCase.owner)
			if err != nil {
				test.Fatalf("register: %v", err)
			}

			if err := service.Heartbeat(test.Context(), instance.ID, testCase.caller); !errors.Is(err, testCase.wantErr) {
				test.Errorf("heartbeat: expected %v, got %v", testCase.wantErr, err)
			}

			if err := service.Unregister(test.Context(), instance.ID, testCase.caller); !errors.Is(err, testCase.wantErr) {
				test.Errorf("unregister: expected %v, got %v", testCase.wantErr, err)
			}

			remaining, err := service.Discover(test.Context(), "orders")
			if err != nil {
				test.Fatalf("discover: %v", err)
			}

			if wantRemaining := testCase.wantErr != nil; (len(remaining) == 1) != wantRemaining {
				test.Errorf("expected the instance to remain %t, got %d instances", wantRemaining, len(remaining))
			}
		})
	}
}

func TestOwnerAppearsInTheLogsOnly(test *testing.T) {
	var logs bytes.Buffer

	service, err := discovery.New(memory.New(), testConfig, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		test.Fatalf("new service: %v", err)
	}

	instance, err := service.Register(test.Context(), registration("orders"), orders)
	if err != nil {
		test.Fatalf("register: %v", err)
	}

	if err := service.Unregister(test.Context(), instance.ID, operator); err != nil {
		test.Fatalf("unregister: %v", err)
	}

	for _, want := range []string{`msg="service registered"`, "owner=orders", `msg="service unregistered"`, "by=operator"} {
		if !strings.Contains(logs.String(), want) {
			test.Errorf("expected the log to contain %q, logs:\n%s", want, logs.String())
		}
	}
}

func TestUnknownInstanceIsNotFoundForEveryCaller(test *testing.T) {
	service := newService(test)

	for _, caller := range []discovery.Caller{orders, operator, anyone, {}} {
		if err := service.Heartbeat(test.Context(), "unknown-id", caller); !errors.Is(err, discovery.ErrInstanceNotFound) {
			test.Errorf("caller %+v: expected not found from heartbeat, got %v", caller, err)
		}

		if err := service.Unregister(test.Context(), "unknown-id", caller); !errors.Is(err, discovery.ErrInstanceNotFound) {
			test.Errorf("caller %+v: expected not found from unregister, got %v", caller, err)
		}
	}
}
