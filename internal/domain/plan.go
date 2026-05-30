package domain

import (
	"context"
	"time"
)

type Plan struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	FollowersQty int    `json:"followers_qty"`
	PriceCents   int    `json:"price_cents"`
	Currency     string `json:"currency"`
	Active       bool   `json:"active"`
	SortOrder    int    `json:"sort_order"`
	// Prices é o preço manual por moeda (currency_code -> valor string).
	// BRL é a base de contabilidade (espelha PriceCents).
	Prices    map[string]string `json:"prices"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type PlanRepository interface {
	ListActive(ctx context.Context) ([]Plan, error)
	ListAll(ctx context.Context) ([]Plan, error)
	GetByID(ctx context.Context, id string) (*Plan, error)
	Create(ctx context.Context, p Plan) error
	Update(ctx context.Context, p Plan) error
	Delete(ctx context.Context, id string) error
	UpsertPrices(ctx context.Context, planID string, prices map[string]string) error
}
