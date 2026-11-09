package http

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

// GetServicesResponse wraps a list of services. It is not used yet:
// GET /services currently returns a bare JSON array.
type GetServicesResponse struct {
	Services []Service `json:"services"`
}

// Service is how a registered instance appears in API responses.
type Service struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// UnregisterRequest is the body of DELETE /unregister.
type UnregisterRequest struct {
	ID string `json:"id"`
}
