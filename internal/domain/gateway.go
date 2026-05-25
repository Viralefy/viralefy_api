package domain

import "time"

type PaymentGateway struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Provider  string            `json:"provider"`
	Active    bool              `json:"active"`
	Config    map[string]string `json:"config"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type GatewayRepository interface {
	ListAll(ctx interface{}) ([]PaymentGateway, error)
	GetByID(ctx interface{}, id string) (*PaymentGateway, error)
	Create(ctx interface{}, g PaymentGateway) error
	Update(ctx interface{}, g PaymentGateway) error
	Delete(ctx interface{}, id string) error
	GetDefaultActive(ctx interface{}) (*PaymentGateway, error)
}
