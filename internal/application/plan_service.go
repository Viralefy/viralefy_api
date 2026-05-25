package application

import (
	"context"

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
	Name         string
	Description  string
	FollowersQty int
	PriceCents   int
	Currency     string
	Active       bool
	SortOrder    int
}

func (s *PlanService) Create(ctx context.Context, in CreatePlanInput) (*domain.Plan, error) {
	if in.Name == "" || in.FollowersQty <= 0 || in.PriceCents <= 0 {
		return nil, domain.ErrInvalidInput
	}
	currency := in.Currency
	if currency == "" {
		currency = "BRL"
	}
	p := domain.Plan{
		ID:           uuid.New().String(),
		Name:         in.Name,
		Description:  in.Description,
		FollowersQty: in.FollowersQty,
		PriceCents:   in.PriceCents,
		Currency:     currency,
		Active:       in.Active,
		SortOrder:    in.SortOrder,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	return &p, nil
}

type UpdatePlanInput struct {
	ID           string
	Name         string
	Description  string
	FollowersQty int
	PriceCents   int
	Currency     string
	Active       bool
	SortOrder    int
}

func (s *PlanService) Update(ctx context.Context, in UpdatePlanInput) (*domain.Plan, error) {
	existing, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		existing.Name = in.Name
	}
	if in.Description != "" {
		existing.Description = in.Description
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
	return existing, nil
}

func (s *PlanService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}
