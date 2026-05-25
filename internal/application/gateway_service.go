package application

import (
	"context"

	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type GatewayService struct {
	repo domain.GatewayRepository
}

func NewGatewayService(repo domain.GatewayRepository) *GatewayService {
	return &GatewayService{repo: repo}
}

func (s *GatewayService) List(ctx context.Context) ([]domain.PaymentGateway, error) {
	return s.repo.ListAll(ctx)
}

type CreateGatewayInput struct {
	Name     string
	Provider string
	Active   bool
	Config   map[string]string
}

func (s *GatewayService) Create(ctx context.Context, in CreateGatewayInput) (*domain.PaymentGateway, error) {
	if in.Name == "" || in.Provider == "" {
		return nil, domain.ErrInvalidInput
	}
	g := domain.PaymentGateway{
		ID:       uuid.New().String(),
		Name:     in.Name,
		Provider: in.Provider,
		Active:   in.Active,
		Config:   in.Config,
	}
	if g.Config == nil {
		g.Config = map[string]string{}
	}
	if err := s.repo.Create(ctx, g); err != nil {
		return nil, err
	}
	return &g, nil
}

type UpdateGatewayInput struct {
	ID       string
	Name     string
	Provider string
	Active   bool
	Config   map[string]string
}

func (s *GatewayService) Update(ctx context.Context, in UpdateGatewayInput) (*domain.PaymentGateway, error) {
	existing, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		existing.Name = in.Name
	}
	if in.Provider != "" {
		existing.Provider = in.Provider
	}
	existing.Active = in.Active
	if in.Config != nil {
		existing.Config = in.Config
	}
	if err := s.repo.Update(ctx, *existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *GatewayService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}
