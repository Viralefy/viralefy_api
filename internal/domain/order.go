package domain

import (
	"context"
	"time"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusPaid      OrderStatus = "paid"
	OrderStatusFailed    OrderStatus = "failed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

type Order struct {
	ID                 string            `json:"id"`
	UserID             string            `json:"user_id"`
	PlanID             string            `json:"plan_id"`
	Status             OrderStatus       `json:"status"`
	AmountCents        int               `json:"amount_cents"`
	Currency           string            `json:"currency"`
	DisplayCurrency    string            `json:"display_currency"`
	DisplayAmount      string            `json:"display_amount"`
	SettlementCurrency string            `json:"settlement_currency"`
	SettlementAmount   string            `json:"settlement_amount"`
	GatewayID          *string           `json:"gateway_id,omitempty"`
	ExternalRef        *string           `json:"external_ref,omitempty"`
	PaymentURL         *string           `json:"payment_url,omitempty"`
	PaymentExtra       map[string]string `json:"payment_extra,omitempty"`
	ProfileID          *string           `json:"profile_id,omitempty"`
	PublicationURL     *string           `json:"publication_url,omitempty"`
	PaymentMethod      string            `json:"payment_method"`     // gateway | credits
	CreditsUsedCents   int               `json:"credits_used_cents"` // se payment_method=credits
	// CustomData carrega snapshot do formulário customizado da categoria
	// (ex.: account recovery — data do banimento, motivo estimado, última
	// publicação). Schema livre por categoria; backend não interpreta, só
	// repassa pro ticket aberto após pagamento.
	CustomData         map[string]any    `json:"custom_data,omitempty"`
	// TicketID linka o pedido ao ticket aberto automaticamente quando
	// `Status` virou `paid` em categorias que abrem ticket (recovery,
	// BMs, perfis).
	TicketID           *string           `json:"ticket_id,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// OrderView é um read-model de pedido enriquecido com dados do plano,
// usado em histórico de compras e listagem admin.
type OrderView struct {
	Order
	PlanName     string `json:"plan_name"`
	PlanCategory string `json:"plan_category"`
}

type OrderRepository interface {
	Create(ctx context.Context, o Order) error
	GetByID(ctx context.Context, id string) (*Order, error)
	GetByExternalRef(ctx context.Context, externalRef string) (*Order, error)
	ListByUser(ctx context.Context, userID string) ([]Order, error)
	ListViewByUser(ctx context.Context, userID string) ([]OrderView, error)
	ListAll(ctx context.Context) ([]Order, error)
	ListAllView(ctx context.Context) ([]OrderView, error)
	UpdateStatus(ctx context.Context, id string, status OrderStatus, externalRef *string) error
	UpdatePayment(ctx context.Context, id, externalRef, paymentURL string, extra map[string]string) error
	// LinkTicket associa um ticket aberto pós-pagamento ao pedido.
	LinkTicket(ctx context.Context, orderID, ticketID string) error
}
