package domain

import "time"

type Plan struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	FollowersQty int       `json:"followers_qty"`
	PriceCents   int       `json:"price_cents"`
	Currency     string    `json:"currency"`
	Active       bool      `json:"active"`
	SortOrder    int       `json:"sort_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type PlanRepository interface {
	ListActive(ctx interface{}) ([]Plan, error)
	ListAll(ctx interface{}) ([]Plan, error)
	GetByID(ctx interface{}, id string) (*Plan, error)
	Create(ctx interface{}, p Plan) error
	Update(ctx interface{}, p Plan) error
	Delete(ctx interface{}, id string) error
}
