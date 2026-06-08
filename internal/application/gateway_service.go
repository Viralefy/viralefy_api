package application

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type GatewayService struct {
	repo domain.GatewayRepository
}

func NewGatewayService(repo domain.GatewayRepository) *GatewayService {
	return &GatewayService{repo: repo}
}

// validProviders fixa o enum aceito pelo backend. Mantém sincronizado
// com o seletor do backoffice (gateways/page.tsx). Heleket/Woovi/ManualPIX
// têm handlers de webhook específicos; qualquer outro provider seria órfão.
var validProviders = map[string]bool{
	"woovi":      true,
	"heleket":    true,
	"manual_pix": true,
}

// validCurrency aceita qualquer ISO 4217 maiúsculo de 3 letras OU códigos
// crypto comuns. O front limita o picker (USDT/USD/EUR/BRL/BTC); aqui só
// barramos lixo (string vazia/minúsculo).
func validCurrencyCode(c string) bool {
	if len(c) < 3 || len(c) > 5 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func (s *GatewayService) List(ctx context.Context) ([]domain.PaymentGateway, error) {
	return s.repo.ListAll(ctx)
}

// GetActiveByProvider expõe lookup do gateway ativo de um provider.
// Usado pelos handlers de webhook para pegar a config (webhook_secret/api_key).
func (s *GatewayService) GetActiveByProvider(ctx context.Context, provider string) (*domain.PaymentGateway, error) {
	return s.repo.GetActiveByProvider(ctx, provider)
}

type CreateGatewayInput struct {
	Name               string            `json:"name"`
	Provider           string            `json:"provider"`
	Active             bool              `json:"active"`
	Config             map[string]string `json:"config"`
	AcceptedCurrencies []string          `json:"accepted_currencies"`
}

// validateGateway centraliza as regras de provider + accepted_currencies.
// Gateway active=true SEM moedas é a pior pegadinha (checkout escolhe ele,
// não encontra moeda compatível, e o pedido morre em "no gateway available").
func validateGateway(provider string, active bool, currencies []string) ([]string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if !validProviders[provider] {
		return nil, domain.ErrInvalidInput
	}
	out := make([]string, 0, len(currencies))
	seen := map[string]bool{}
	for _, c := range currencies {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c == "" || seen[c] {
			continue
		}
		if !validCurrencyCode(c) {
			return nil, domain.ErrInvalidInput
		}
		seen[c] = true
		out = append(out, c)
	}
	if active && len(out) == 0 {
		return nil, domain.ErrInvalidInput
	}
	return out, nil
}

func (s *GatewayService) Create(ctx context.Context, in CreateGatewayInput) (*domain.PaymentGateway, error) {
	if in.Name == "" || in.Provider == "" {
		return nil, domain.ErrInvalidInput
	}
	ccy, err := validateGateway(in.Provider, in.Active, in.AcceptedCurrencies)
	if err != nil {
		return nil, err
	}
	g := domain.PaymentGateway{
		ID:                 uuid.New().String(),
		Name:               in.Name,
		Provider:           strings.ToLower(strings.TrimSpace(in.Provider)),
		Active:             in.Active,
		Config:             in.Config,
		AcceptedCurrencies: ccy,
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
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Provider           string            `json:"provider"`
	Active             bool              `json:"active"`
	Config             map[string]string `json:"config"`
	AcceptedCurrencies []string          `json:"accepted_currencies"`
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
		existing.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	}
	existing.Active = in.Active
	if in.Config != nil {
		existing.Config = in.Config
	}
	if in.AcceptedCurrencies != nil {
		existing.AcceptedCurrencies = in.AcceptedCurrencies
	}
	ccy, err := validateGateway(existing.Provider, existing.Active, existing.AcceptedCurrencies)
	if err != nil {
		return nil, err
	}
	existing.AcceptedCurrencies = ccy
	if err := s.repo.Update(ctx, *existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *GatewayService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}
