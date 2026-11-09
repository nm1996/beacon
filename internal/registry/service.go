package registry

import "time"

type Service struct {
	ID            string
	Name          string
	Address       string
	Port          int
	RegisteredAt  time.Time
	LastHeartbeat time.Time
}
