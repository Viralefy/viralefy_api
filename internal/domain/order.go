package domain

import "time"

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusPaid      OrderStatus = "paid"
	OrderStatusFailed    OrderStatus = "failed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

type Order struct {
	ID          string      `json:"id"`
	UserID      string      `json:"user_id"`
	PlanID      string      `json:"plan_id"`
	Status      OrderStatus `json:"status"`
	AmountCents int         `json:"amount_cents"`
	Currency    string      `json:"currency"`
	GatewayID   *string     `json:"gateway_id,omitempty"`
	ExternalRef *string     `json:"external_ref,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type OrderRepository interface {
	Create(ctx interface{}, o Order) error
	GetByID(ctx interface{}, id string) (*Order, error)
	ListByUser(ctx interface{}, userID string) ([]Order, error)
	ListAll(ctx interface{}) ([]Order, error)
	UpdateStatus(ctx interface{}, id string, status OrderStatus, externalRef *string) error
}
