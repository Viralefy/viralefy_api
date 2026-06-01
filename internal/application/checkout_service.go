package application

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
	"github.com/viralefy/viralefy_api/internal/infrastructure/observability"
	"golang.org/x/crypto/bcrypt"
)

type CheckoutService struct {
	users      domain.UserRepository
	plans      domain.PlanRepository
	orders     domain.OrderRepository
	gateways   domain.GatewayRepository
	profiles   domain.ProfileRepository
	currencies *CurrencyService
	credits    *CreditService
	email      EmailSender
	payments   *PaymentRegistry
	siteURL    string
}

func NewCheckoutService(
	users domain.UserRepository,
	plans domain.PlanRepository,
	orders domain.OrderRepository,
	gateways domain.GatewayRepository,
	profiles domain.ProfileRepository,
	currencies *CurrencyService,
	credits *CreditService,
	email EmailSender,
	payments *PaymentRegistry,
	siteURL string,
) *CheckoutService {
	return &CheckoutService{
		users: users, plans: plans, orders: orders, gateways: gateways,
		profiles: profiles, currencies: currencies, credits: credits,
		email: email, payments: payments, siteURL: siteURL,
	}
}

type CheckoutInput struct {
	PlanID          string
	Email           string
	Name            string
	DisplayCurrency string
	// Alvo do serviço — um dos dois conforme plan.target_type:
	ProfileID      string // se target_type == profile (perfil já cadastrado)
	NewProfile     *NewProfileInline
	PublicationURL string // se target_type == publication
	// CustomData carrega o snapshot do formulário customizado da categoria
	// (Account Recovery, BMs, perfis). Schema livre; é guardado na order
	// e replayed no ticket aberto após pagamento.
	CustomData     map[string]any
	// Pagamento:
	PaymentMethod string // "gateway" (default) ou "credits". credits exige usuário logado com saldo
	UserID        string // setado pelo handler quando usuário está logado; obrigatório p/ credits
}

// NewProfileInline permite o usuário criar um perfil "no ato" do checkout
// (sem precisar ir antes em /account/profiles).
type NewProfileInline struct {
	Platform    string
	Handle      string
	DisplayName string
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
	GatewayProvider    string             `json:"gateway_provider,omitempty"`
	PaymentURL         string             `json:"payment_url,omitempty"`
	PaymentExtra       map[string]string  `json:"payment_extra,omitempty"`
	PaymentMethod      string             `json:"payment_method"` // gateway | credits
	CreditsUsedCents   int                `json:"credits_used_cents,omitempty"`
	CreditBalanceCents int64              `json:"credit_balance_cents,omitempty"`
}

func (s *CheckoutService) Checkout(ctx context.Context, in CheckoutInput) (*CheckoutResult, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Name == "" || in.PlanID == "" {
		return nil, domain.ErrInvalidInput
	}
	if in.PaymentMethod == "" {
		in.PaymentMethod = "gateway"
	}
	if in.PaymentMethod != "gateway" && in.PaymentMethod != "credits" {
		return nil, domain.ErrInvalidInput
	}

	plan, err := s.plans.GetByID(ctx, in.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.Active {
		return nil, domain.ErrInvalidInput
	}

	// Resolve o alvo (perfil ou URL) e VALIDA contra o tipo do plano —
	// primeira defesa contra "mandar serviço errado".
	profileID, publicationURL, err := s.resolveTarget(ctx, plan, in)
	if err != nil {
		return nil, err
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
		if in.PaymentMethod == "credits" {
			// Não dá pra pagar com créditos sem ter conta.
			return nil, domain.ErrUnauthorized
		}
		generatedPassword = GeneratePassword()
		hash, err := bcrypt.GenerateFromPassword([]byte(generatedPassword), 12)
		if err != nil {
			return nil, err
		}
		userID = uuid.New().String()
		if err := s.users.Create(ctx, domain.User{
			ID: userID, Email: in.Email, Name: in.Name, Instagram: "",
			PasswordHash: string(hash),
		}); err != nil {
			return nil, err
		}
		accountCreated = true
	}
	if in.UserID != "" && in.UserID != userID {
		// se o token de usuário diz outra coisa, força o user do token (segurança)
		userID = in.UserID
	}

	// Se o handler quer criar um perfil inline pro usuário logado:
	if in.NewProfile != nil && profileID == "" && plan.TargetType == "profile" {
		platform := domain.Platform(in.NewProfile.Platform)
		if err := ValidateHandle(platform, in.NewProfile.Handle); err != nil {
			return nil, domain.ErrInvalidInput
		}
		np := domain.Profile{
			ID:          uuid.New().String(),
			UserID:      userID,
			Platform:    platform,
			Handle:      NormalizeHandle(in.NewProfile.Handle),
			DisplayName: in.NewProfile.DisplayName,
			Verified:    true,
		}
		if err := s.profiles.Create(ctx, np); err != nil {
			return nil, err
		}
		profileID = np.ID
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
		PaymentMethod:      in.PaymentMethod,
		CustomData:         in.CustomData,
	}
	if profileID != "" {
		order.ProfileID = &profileID
	}
	if publicationURL != "" {
		order.PublicationURL = &publicationURL
	}

	// ---------- Caminho A: pagamento com créditos ----------
	if in.PaymentMethod == "credits" {
		// Conta + saldo.
		acct, err := s.credits.Balance(ctx, userID)
		if err != nil {
			return nil, err
		}
		if acct.BalanceCents < int64(plan.PriceCents) {
			return nil, domain.ErrInvalidInput // saldo insuficiente — front trata
		}
		// Pedido já entra como pago (não há cobrança externa).
		order.Status = domain.OrderStatusPaid
		order.CreditsUsedCents = plan.PriceCents
		if err := s.orders.Create(ctx, order); err != nil {
			return nil, err
		}
		// Debita do ledger (atômico no repo).
		newAcct, err := s.credits.Spend(ctx, userID, int64(plan.PriceCents), "Pedido "+plan.Name, &orderID)
		if err != nil {
			return nil, err
		}
		emailSent := s.sendCheckoutEmail(ctx, in.Email, in.Name, *plan, quote, nil, "", nil, generatedPassword, accountCreated, true)

		return &CheckoutResult{
			OrderID: orderID, Status: order.Status, PlanName: plan.Name,
			DisplayCurrency: quote.DisplayCurrency, DisplaySymbol: quote.DisplaySymbol, DisplayAmount: quote.DisplayAmount,
			SettlementCurrency: quote.SettlementCurrency, SettlementSymbol: quote.SettlementSymbol, SettlementAmount: quote.SettlementAmount,
			AccountCreated: accountCreated, Email: in.Email, EmailSent: emailSent,
			PaymentMethod: "credits", CreditsUsedCents: plan.PriceCents,
			CreditBalanceCents: newAcct.BalanceCents,
		}, nil
	}

	// ---------- Caminho B: pagamento via gateway (padrão) ----------
	gw := s.pickGateway(ctx, quote.SettlementCurrency)
	if gw != nil {
		order.GatewayID = &gw.ID
	}
	if err := s.orders.Create(ctx, order); err != nil {
		return nil, err
	}

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
				observability.FromContext(ctx).Warn("checkout: payment provider failed",
					"provider", gw.Provider,
					"error", perr.Error(),
				)
			} else {
				paymentURL = charge.PaymentURL
				paymentExtra = charge.Extra
				_ = s.orders.UpdatePayment(ctx, orderID, charge.ExternalRef, charge.PaymentURL, charge.Extra)
			}
		}
	}

	emailSent := s.sendCheckoutEmail(ctx, in.Email, in.Name, *plan, quote, gw, paymentURL, paymentExtra, generatedPassword, accountCreated, false)

	return &CheckoutResult{
		OrderID: orderID, Status: domain.OrderStatusPending, PlanName: plan.Name,
		DisplayCurrency: quote.DisplayCurrency, DisplaySymbol: quote.DisplaySymbol, DisplayAmount: quote.DisplayAmount,
		SettlementCurrency: quote.SettlementCurrency, SettlementSymbol: quote.SettlementSymbol, SettlementAmount: quote.SettlementAmount,
		AccountCreated: accountCreated, Email: in.Email, EmailSent: emailSent,
		GatewayProvider: provider, PaymentURL: paymentURL, PaymentExtra: paymentExtra,
		PaymentMethod: "gateway",
	}, nil
}

// resolveTarget valida que o alvo informado bate com plan.target_type e
// retorna profileID/URL apropriados pra persistir no pedido.
func (s *CheckoutService) resolveTarget(ctx context.Context, plan *domain.Plan, in CheckoutInput) (string, string, error) {
	switch plan.TargetType {
	case "profile", "":
		// Aceita ProfileID existente OU NewProfile inline. Pelo menos um.
		if in.ProfileID != "" {
			p, err := s.profiles.GetByID(ctx, in.ProfileID)
			if err != nil {
				return "", "", domain.ErrInvalidInput
			}
			// Confere plataforma: serviço de TikTok não pode ir num perfil IG.
			if plan.Platform != "" && string(p.Platform) != plan.Platform {
				return "", "", domain.ErrInvalidInput
			}
			return p.ID, "", nil
		}
		if in.NewProfile != nil {
			// Plataforma do inline tem que casar com a do plano.
			if plan.Platform != "" && in.NewProfile.Platform != plan.Platform {
				return "", "", domain.ErrInvalidInput
			}
			return "", "", nil // será criado depois (precisamos do userID)
		}
		return "", "", domain.ErrInvalidInput
	case "publication":
		if in.PublicationURL == "" {
			return "", "", domain.ErrInvalidInput
		}
		platform := domain.Platform(plan.Platform)
		if platform == "" {
			platform = domain.PlatformInstagram
		}
		if err := ValidatePublicationURL(platform, in.PublicationURL); err != nil {
			return "", "", domain.ErrInvalidInput
		}
		return "", strings.TrimSpace(in.PublicationURL), nil
	}
	return "", "", domain.ErrInvalidInput
}

// pickGateway escolhe o gateway adequado para a moeda de liquidação.
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
	password string, accountCreated bool, paidWithCredits bool,
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
	if paidWithCredits {
		data.FallbackInstructions = "Pagamento com créditos confirmado. Seu pedido já está em produção."
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
		observability.FromContext(ctx).Error("checkout: render email failed", "error", err.Error())
		return false
	}
	if err := s.email.Send(ctx, EmailMessage{To: to, Subject: subject, TextBody: text, HTMLBody: html}); err != nil {
		observability.FromContext(ctx).Error("checkout: send email failed",
			"to_masked", observability.MaskEmail(to),
			"error", err.Error(),
		)
		return false
	}
	return true
}
