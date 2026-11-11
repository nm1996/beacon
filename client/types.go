package client

import "time"

// Registration describes an instance to register.
type Registration struct {
	// Name groups instances of the same service, such as "orders".
	Name string
	// Address is the host name or IP address where the instance is reachable.
	Address string
	// Port is the TCP port the instance listens on.
	Port int
}

// Registered is what Beacon returns for a new instance.
type Registered struct {
	// ID identifies the instance in later calls.
	ID string
	// TTL is how long the instance may go without a heartbeat before Beacon
	// removes it. Sending a heartbeat every TTL/3 leaves room for misses.
	TTL time.Duration
}

// Instance is a registered instance as returned by Discover.
type Instance struct {
	ID            string
	Name          string
	Address       string
	Port          int
	LastHeartbeat time.Time
}

// Health is what Beacon reports about itself.
type Health struct {
	// Status is "ok" while Beacon is serving requests.
	Status string `json:"status"`
	// Version is the version of the running server.
	Version string `json:"version"`
}

// registerRequest is the body of POST /api/v1/services.
type registerRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// registerResponse is the body Beacon returns for a registration.
type registerResponse struct {
	ID         string `json:"id"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// instanceResponse is one element of the GET /api/v1/services array. Its
// fields match Instance exactly, so it converts directly.
type instanceResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	Port          int       `json:"port"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}
