package http

type RegisterRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type RegisterResponse struct {
	ID string `json:"id"`
}

type HeartbeatRequest struct {
	ID string `json:"id"`
}

type GetServicesResponse struct {
	Services []Service `json:"services"`
}

type Service struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type UnregisterRequest struct {
	ID string `json:"id"`
}
