package application

import (
	"context"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

type CheckoutService struct {
	users      domain.UserRepository
	plans      domain.PlanRepository
	orders     domain.OrderRepository
	gateways   domain.GatewayRepository
	currencies *CurrencyService
	email      EmailSender
	payments   *PaymentRegistry
	siteURL    string // base URL pública (loja) — usado no e-mail (logo + link de suporte)
}

func NewCheckoutService(
	users domain.UserRepository,
	plans domain.PlanRepository,
	orders domain.OrderRepository,
	gateways domain.GatewayRepository,
	currencies *CurrencyService,
	email EmailSender,
	payments *PaymentRegistry,
	siteURL string,
) *CheckoutService {
	return &CheckoutService{
		users: users, plans: plans, orders: orders, gateways: gateways,
		currencies: currencies, email: email, payments: payments,
		siteURL: siteURL,
	}
}

type CheckoutInput struct {
	PlanID          string
	Email           string
	Name            string
	Instagram       string
	DisplayCurrency string
}

type CheckoutResult struct {
	OrderID            string             `json:"order_id"`
	Status             domain.OrderStatus `json:"status"`
	PlanName           string             `json:"plan_name"`
	DisplayCurrency    string             `json:"display_currency"`
	DisplaySymbol      string             `json:"display_symbol"`
	DisplayAmount      string             `json:"display_amount"`
	SettlementCurrency string             `json:"settlement_currency"`
	SettlementSymbol   string             `json:"settlement_symbol"`
	SettlementAmount   string             `json:"settlement_amount"`
	AccountCreated     bool               `json:"account_created"`
	Email              string             `json:"email"`
	EmailSent          bool               `json:"email_sent"`
	GatewayProvider    string             `json:"gateway_provider"`
	PaymentURL         string             `json:"payment_url,omitempty"`
	PaymentExtra       map[string]string  `json:"payment_extra,omitempty"`
}

func (s *CheckoutService) Checkout(ctx context.Context, in CheckoutInput) (*CheckoutResult, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.Instagram = strings.TrimSpace(strings.TrimPrefix(in.Instagram, "@"))
	if in.Email == "" || in.Name == "" || in.Instagram == "" || in.PlanID == "" {
		return nil, domain.ErrInvalidInput
	}

	plan, err := s.plans.GetByID(ctx, in.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.Active {
		return nil, domain.ErrInvalidInput
	}

	quote, err := s.currencies.QuoteForPlan(ctx, plan.Prices, plan.PriceCents, in.DisplayCurrency)
	if err != nil {
		return nil, err
	}

	// Autocadastro: cria conta se não existir e gera senha; senão reaproveita.
	existing, _ := s.users.GetByEmail(ctx, in.Email)
	var userID, generatedPassword string
	accountCreated := false
	if existing != nil {
		userID = existing.ID
	} else {
		generatedPassword = GeneratePassword()
		hash, err := bcrypt.GenerateFromPassword([]byte(generatedPassword), 12)
		if err != nil {
			return nil, err
		}
		userID = uuid.New().String()
		if err := s.users.Create(ctx, domain.User{
			ID: userID, Email: in.Email, Name: in.Name, Instagram: in.Instagram,
			PasswordHash: string(hash),
		}); err != nil {
			return nil, err
		}
		accountCreated = true
	}

	gw := s.pickGateway(ctx, quote.SettlementCurrency)
	var gatewayID *string
	if gw != nil {
		gatewayID = &gw.ID
	}

	orderID := uuid.New().String()
	order := domain.Order{
		ID:                 orderID,
		UserID:             userID,
		PlanID:             plan.ID,
		Status:             domain.OrderStatusPending,
		AmountCents:        plan.PriceCents,
		Currency:           plan.Currency,
		DisplayCurrency:    quote.DisplayCurrency,
		DisplayAmount:      quote.DisplayAmount,
		SettlementCurrency: quote.SettlementCurrency,
		SettlementAmount:   quote.SettlementAmount,
		GatewayID:          gatewayID,
	}
	if err := s.orders.Create(ctx, order); err != nil {
		return nil, err
	}

	// Cria cobrança no provider (PIX ou cripto) e persiste no pedido.
	provider := ""
	var paymentURL string
	var paymentExtra map[string]string
	if gw != nil {
		provider = gw.Provider
		if p, ok := s.payments.Get(gw.Provider); ok {
			charge, perr := p.CreateCharge(ctx, PaymentChargeInput{
				OrderID:     orderID,
				Description: plan.Name,
				Amount:      quote.SettlementAmount,
				Currency:    quote.SettlementCurrency,
				Customer:    PaymentCustomer{Name: in.Name, Email: in.Email},
				Config:      gw.Config,
			})
			if perr != nil {
				log.Printf("checkout: provider %s falhou: %v", gw.Provider, perr)
			} else {
				paymentURL = charge.PaymentURL
				paymentExtra = charge.Extra
				_ = s.orders.UpdatePayment(ctx, orderID, charge.ExternalRef, charge.PaymentURL, charge.Extra)
			}
		}
	}

	emailSent := s.sendCheckoutEmail(ctx, in.Email, in.Name, *plan, quote, gw, paymentURL, paymentExtra, generatedPassword, accountCreated)

	return &CheckoutResult{
		OrderID:            orderID,
		Status:             domain.OrderStatusPending,
		PlanName:           plan.Name,
		DisplayCurrency:    quote.DisplayCurrency,
		DisplaySymbol:      quote.DisplaySymbol,
		DisplayAmount:      quote.DisplayAmount,
		SettlementCurrency: quote.SettlementCurrency,
		SettlementSymbol:   quote.SettlementSymbol,
		SettlementAmount:   quote.SettlementAmount,
		AccountCreated:     accountCreated,
		Email:              in.Email,
		EmailSent:          emailSent,
		GatewayProvider:    provider,
		PaymentURL:         paymentURL,
		PaymentExtra:       paymentExtra,
	}, nil
}

// pickGateway escolhe o gateway adequado para a moeda de liquidação. Roteia
// BRL → woovi (PIX), USDT/BTC → heleket (cripto), demais → default ativo.
func (s *CheckoutService) pickGateway(ctx context.Context, settlement string) *domain.PaymentGateway {
	candidate := ""
	switch strings.ToUpper(settlement) {
	case "BRL":
		candidate = "woovi"
	case "USDT", "BTC":
		candidate = "heleket"
	}
	if candidate != "" {
		if g, err := s.gateways.GetActiveByProvider(ctx, candidate); err == nil && g != nil {
			return g
		}
	}
	g, _ := s.gateways.GetDefaultActive(ctx)
	return g
}

func (s *CheckoutService) sendCheckoutEmail(
	ctx context.Context, to, name string, plan domain.Plan, q Quote, gw *domain.PaymentGateway,
	paymentURL string, paymentExtra map[string]string,
	password string, accountCreated bool,
) bool {
	data := CheckoutEmailData{
		SiteURL:              s.siteURL,
		Name:                 name,
		Email:                to,
		PlanName:             plan.Name,
		DisplayCurrency:      q.DisplayCurrency,
		DisplaySymbol:        q.DisplaySymbol,
		DisplayAmount:        q.DisplayAmount,
		SettlementCurrency:   q.SettlementCurrency,
		SettlementAmount:     q.SettlementAmount,
		AccountCreated:       accountCreated,
		Password:             password,
		PaymentURL:           paymentURL,
		FallbackInstructions: "As instruções de pagamento seguem em breve. Em caso de dúvida, abra um ticket.",
	}
	if paymentExtra != nil {
		data.BrCode = paymentExtra["br_code"]
		data.QrImage = paymentExtra["qr_code_image"]
		data.CryptoAddress = paymentExtra["address"]
		data.CryptoNetwork = paymentExtra["network"]
	}
	if data.PixKey == "" && gw != nil && q.SettlementCurrency == "BRL" {
		data.PixKey = gw.Config["pix_key"]
	}

	subject, html, text, err := BuildCheckoutEmail(data)
	if err != nil {
		log.Printf("checkout: erro renderizando e-mail: %v", err)
		return false
	}
	if err := s.email.Send(ctx, EmailMessage{To: to, Subject: subject, TextBody: text, HTMLBody: html}); err != nil {
		log.Printf("checkout: falha ao enviar e-mail para %s: %v", to, err)
		return false
	}
	return true
}
