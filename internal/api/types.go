package api

import "time"

// RegisterRequest is the body of POST /api/v1/services.
type RegisterRequest struct {
	// ID is optional: the instance ID to use, so registering again keeps it.
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// RegisterResponse is returned after a successful registration. Clients keep
// the ID to send heartbeats and to unregister later, and must send a
// heartbeat at least every TTLSeconds; a third of that is a safe pace.
type RegisterResponse struct {
	ID         string `json:"id"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// Service is how a registered instance appears in API responses.
type Service struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	Port          int       `json:"port"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// HealthResponse is the body of GET /health.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ErrorResponse is the body of every error response.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes what went wrong. Code is stable and meant for
// programs; Message is meant for people and may change.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Node is one live Beacon node, as GET /api/v1/nodes lists it.
type Node struct {
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	LastSeen  time.Time `json:"last_seen"`
}
