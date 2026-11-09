package api

// RegisterRequest is the body of POST /register.
type RegisterRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// RegisterResponse is returned after a successful registration. Clients keep
// the ID to send heartbeats and to unregister later.
type RegisterResponse struct {
	ID string `json:"id"`
}

// HeartbeatRequest is the body of POST /heartbeat.
type HeartbeatRequest struct {
	ID string `json:"id"`
}

// Service is how a registered instance appears in API responses.
type Service struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// HealthResponse is the body of GET /health.
type HealthResponse struct {
	Status string `json:"status"`
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

// UnregisterRequest is the body of DELETE /unregister.
type UnregisterRequest struct {
	ID string `json:"id"`
}
