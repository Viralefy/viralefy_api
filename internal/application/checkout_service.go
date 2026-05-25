package application

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

type CheckoutService struct {
	users    domain.UserRepository
	plans    domain.PlanRepository
	orders   domain.OrderRepository
	gateways domain.GatewayRepository
}

func NewCheckoutService(
	users domain.UserRepository,
	plans domain.PlanRepository,
	orders domain.OrderRepository,
	gateways domain.GatewayRepository,
) *CheckoutService {
	return &CheckoutService{users: users, plans: plans, orders: orders, gateways: gateways}
}

type CheckoutInput struct {
	PlanID    string
	Email     string
	Name      string
	Instagram string
	Password  string
}

type CheckoutResult struct {
	UserID   string             `json:"user_id"`
	OrderID  string             `json:"order_id"`
	Status   domain.OrderStatus `json:"status"`
	Amount   int                `json:"amount"`
	Currency string             `json:"currency"`
}

func (s *CheckoutService) Checkout(ctx context.Context, in CheckoutInput) (*CheckoutResult, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.Instagram = strings.TrimSpace(strings.TrimPrefix(in.Instagram, "@"))
	if in.Email == "" || in.Name == "" || in.Instagram == "" || in.Password == "" || in.PlanID == "" {
		return nil, domain.ErrInvalidInput
	}
	if len(in.Password) < 8 {
		return nil, domain.ErrInvalidInput
	}

	plan, err := s.plans.GetByID(ctx, in.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.Active {
		return nil, domain.ErrInvalidInput
	}

	existing, _ := s.users.GetByEmail(ctx, in.Email)
	var userID string
	if existing != nil {
		userID = existing.ID
	} else {
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if err != nil {
			return nil, err
		}
		userID = uuid.New().String()
		u := domain.User{
			ID:           userID,
			Email:        in.Email,
			Name:         in.Name,
			Instagram:    in.Instagram,
			PasswordHash: string(hash),
		}
		if err := s.users.Create(ctx, u); err != nil {
			return nil, err
		}
	}

	gw, _ := s.gateways.GetDefaultActive(ctx)
	var gatewayID *string
	if gw != nil {
		gatewayID = &gw.ID
	}

	orderID := uuid.New().String()
	order := domain.Order{
		ID:          orderID,
		UserID:      userID,
		PlanID:      plan.ID,
		Status:      domain.OrderStatusPending,
		AmountCents: plan.PriceCents,
		Currency:    plan.Currency,
		GatewayID:   gatewayID,
	}
	if err := s.orders.Create(ctx, order); err != nil {
		return nil, err
	}

	return &CheckoutResult{
		UserID:   userID,
		OrderID:  orderID,
		Status:   domain.OrderStatusPending,
		Amount:   plan.PriceCents,
		Currency: plan.Currency,
	}, nil
}
