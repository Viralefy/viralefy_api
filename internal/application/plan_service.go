package application

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type PlanService struct {
	repo domain.PlanRepository
}

func NewPlanService(repo domain.PlanRepository) *PlanService {
	return &PlanService{repo: repo}
}

func (s *PlanService) ListPublic(ctx context.Context) ([]domain.Plan, error) {
	return s.repo.ListActive(ctx)
}

func (s *PlanService) ListAdmin(ctx context.Context) ([]domain.Plan, error) {
	return s.repo.ListAll(ctx)
}

type CreatePlanInput struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Category     string            `json:"category"`
	FollowersQty int               `json:"followers_qty"`
	PriceCents   int               `json:"price_cents"`
	Currency     string            `json:"currency"`
	Active       bool              `json:"active"`
	SortOrder    int               `json:"sort_order"`
	Prices       map[string]string `json:"prices"` // preço manual por moeda
}

func (s *PlanService) Create(ctx context.Context, in CreatePlanInput) (*domain.Plan, error) {
	// Preço base BRL pode vir em PriceCents ou em Prices["BRL"].
	if cents, ok := brlCents(in.Prices); ok {
		in.PriceCents = cents
	}
	if in.Name == "" || in.FollowersQty <= 0 || in.PriceCents <= 0 {
		return nil, domain.ErrInvalidInput
	}
	currency := in.Currency
	if currency == "" {
		currency = "BRL"
	}
	category := in.Category
	if category == "" {
		category = "seguidores"
	}
	p := domain.Plan{
		ID:           uuid.New().String(),
		Name:         in.Name,
		Description:  in.Description,
		Category:     category,
		FollowersQty: in.FollowersQty,
		PriceCents:   in.PriceCents,
		Currency:     currency,
		Active:       in.Active,
		SortOrder:    in.SortOrder,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	prices := withBRL(in.Prices, in.PriceCents)
	if err := s.repo.UpsertPrices(ctx, p.ID, prices); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, p.ID)
}

type UpdatePlanInput struct {
	ID           string            `json:"-"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Category     string            `json:"category"`
	FollowersQty int               `json:"followers_qty"`
	PriceCents   int               `json:"price_cents"`
	Currency     string            `json:"currency"`
	Active       bool              `json:"active"`
	SortOrder    int               `json:"sort_order"`
	Prices       map[string]string `json:"prices"`
}

func (s *PlanService) Update(ctx context.Context, in UpdatePlanInput) (*domain.Plan, error) {
	existing, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if cents, ok := brlCents(in.Prices); ok {
		in.PriceCents = cents
	}
	if in.Name != "" {
		existing.Name = in.Name
	}
	if in.Description != "" {
		existing.Description = in.Description
	}
	if in.Category != "" {
		existing.Category = in.Category
	}
	if in.FollowersQty > 0 {
		existing.FollowersQty = in.FollowersQty
	}
	if in.PriceCents > 0 {
		existing.PriceCents = in.PriceCents
	}
	if in.Currency != "" {
		existing.Currency = in.Currency
	}
	existing.Active = in.Active
	existing.SortOrder = in.SortOrder
	if err := s.repo.Update(ctx, *existing); err != nil {
		return nil, err
	}
	if len(in.Prices) > 0 {
		if err := s.repo.UpsertPrices(ctx, existing.ID, withBRL(in.Prices, existing.PriceCents)); err != nil {
			return nil, err
		}
	}
	return s.repo.GetByID(ctx, existing.ID)
}

func (s *PlanService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// brlCents extrai o preço BRL do mapa de preços manuais (ex.: "9.90" -> 990).
func brlCents(prices map[string]string) (int, bool) {
	v, ok := prices["BRL"]
	if !ok || v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
	if err != nil {
		return 0, false
	}
	return int(math.Round(f * 100)), true
}

// withBRL garante que BRL esteja presente no mapa, derivando de price_cents.
func withBRL(prices map[string]string, cents int) map[string]string {
	out := map[string]string{}
	for k, v := range prices {
		out[k] = v
	}
	if _, ok := out["BRL"]; !ok {
		out["BRL"] = strconv.FormatFloat(float64(cents)/100.0, 'f', 2, 64)
	}
	return out
}
