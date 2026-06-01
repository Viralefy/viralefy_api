package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/viralefy/viralefy_api/internal/domain"
	"github.com/viralefy/viralefy_api/internal/infrastructure/observability"
)

// CategoriesOpeningTicket lista as categorias cujo pedido pago abre um
// ticket de suporte automaticamente. Espelho de TICKET_OPENING_CATEGORIES
// no front. Mudanças aqui devem espelhar lá.
//
// O ticket é aberto com prioridade "normal" (default do TicketService.Open);
// admin pode subir pra "high" via backoffice quando triagem confirmar
// urgência.
var CategoriesOpeningTicket = map[string]bool{
	"recuperacao_perfil": true,
	"bms_facebook":       true,
	"perfis_redes":       true,
}

// PaymentReceiver é o ponto único de entrada para confirmações de pagamento
// (webhook ou ação manual do admin). Idempotente: chamadas repetidas para
// o mesmo external_ref / id já pago são no-op.
type PaymentReceiver struct {
	invoices   domain.InvoiceRepository
	orders     domain.OrderRepository
	plans      domain.PlanRepository
	tickets    *TicketService
	invoiceSvc *InvoiceService
}

func NewPaymentReceiver(
	invoices domain.InvoiceRepository,
	orders domain.OrderRepository,
	plans domain.PlanRepository,
	tickets *TicketService,
	invoiceSvc *InvoiceService,
) *PaymentReceiver {
	return &PaymentReceiver{
		invoices:   invoices,
		orders:     orders,
		plans:      plans,
		tickets:    tickets,
		invoiceSvc: invoiceSvc,
	}
}

// ConfirmByExternalRef tenta achar invoice OU order com aquele external_ref
// e marca como paga. Retorna o "tipo" identificado ("invoice", "order" ou
// vazio) para o caller logar. Erros de lookup são ignorados (provider pode
// mandar webhook para algo que já foi processado / não existe).
func (r *PaymentReceiver) ConfirmByExternalRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("external_ref vazio")
	}
	if inv, err := r.invoices.GetByExternalRef(ctx, ref); err == nil && inv != nil {
		if inv.Status == domain.InvoiceStatusPaid {
			return "invoice", nil
		}
		if _, err := r.invoiceSvc.AdminMarkPaid(ctx, inv.ID); err != nil {
			return "invoice", err
		}
		observability.FromContext(ctx).Info("invoice confirmed",
			"component", "payment_receiver",
			"invoice_id", inv.ID,
			"external_ref", ref,
		)
		return "invoice", nil
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}

	if ord, err := r.orders.GetByExternalRef(ctx, ref); err == nil && ord != nil {
		if ord.Status == domain.OrderStatusPaid {
			return "order", nil
		}
		extRef := ref
		if err := r.orders.UpdateStatus(ctx, ord.ID, domain.OrderStatusPaid, &extRef); err != nil {
			return "order", err
		}
		observability.FromContext(ctx).Info("order marked paid",
			"component", "payment_receiver",
			"order_id", ord.ID,
			"external_ref", ref,
		)
		// Refresh + handoff manual em categorias que abrem ticket.
		if refreshed, err := r.orders.GetByID(ctx, ord.ID); err == nil && refreshed != nil {
			r.maybeOpenTicket(ctx, refreshed)
		}
		return "order", nil
	}
	return "", nil
}

// MarkOrderPaid força a marcação direta (uso do admin via backoffice quando
// webhook não está configurado). Idempotente.
func (r *PaymentReceiver) MarkOrderPaid(ctx context.Context, orderID string) error {
	ord, err := r.orders.GetByID(ctx, orderID)
	if err != nil {
		return err
	}
	if ord.Status == domain.OrderStatusPaid {
		return nil
	}
	if err := r.orders.UpdateStatus(ctx, orderID, domain.OrderStatusPaid, ord.ExternalRef); err != nil {
		return err
	}
	if refreshed, err := r.orders.GetByID(ctx, ord.ID); err == nil && refreshed != nil {
		r.maybeOpenTicket(ctx, refreshed)
	}
	return nil
}

// maybeOpenTicket abre ticket de suporte automaticamente para categorias
// com handoff manual (Account Recovery, BMs, perfis). Idempotente — só abre
// se o pedido ainda não tem ticket_id. Falhas não bloqueiam a confirmação
// (logamos e seguimos).
func (r *PaymentReceiver) maybeOpenTicket(ctx context.Context, ord *domain.Order) {
	if ord.TicketID != nil && *ord.TicketID != "" {
		return
	}
	if r.tickets == nil || r.plans == nil {
		return
	}
	plan, err := r.plans.GetByID(ctx, ord.PlanID)
	if err != nil || plan == nil {
		return
	}
	if !CategoriesOpeningTicket[plan.Category] {
		return
	}

	subject := fmt.Sprintf("[%s] Order #%s — %s", plan.Category, ord.ID[:8], plan.Name)
	body := r.formatTicketBody(ord, plan)

	t, err := r.tickets.Open(ctx, OpenTicketInput{
		UserID:  ord.UserID,
		Subject: subject,
		Body:    body,
		OrderID: &ord.ID,
	})
	if err != nil {
		observability.FromContext(ctx).Error("auto-open ticket failed",
			"component", "payment_receiver",
			"order_id", ord.ID,
			"category", plan.Category,
			"error", err.Error(),
		)
		return
	}
	if err := r.orders.LinkTicket(ctx, ord.ID, t.ID); err != nil {
		observability.FromContext(ctx).Warn("ticket linked but order LinkTicket failed",
			"order_id", ord.ID,
			"ticket_id", t.ID,
			"error", err.Error(),
		)
		return
	}
	observability.FromContext(ctx).Info("ticket auto-opened",
		"component", "payment_receiver",
		"order_id", ord.ID,
		"ticket_id", t.ID,
		"category", plan.Category,
	)
}

// formatTicketBody monta o corpo inicial do ticket: dados do pedido + dump
// do form (CustomData) em chave=valor, ordenado pra ficar legível pro admin.
func (r *PaymentReceiver) formatTicketBody(ord *domain.Order, plan *domain.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Order #%s — %s\n", ord.ID, plan.Name)
	fmt.Fprintf(&b, "Category: %s\n", plan.Category)
	fmt.Fprintf(&b, "Amount: %s %s (display %s %s)\n",
		ord.SettlementAmount, ord.SettlementCurrency,
		ord.DisplayAmount, ord.DisplayCurrency)
	if ord.ProfileID != nil {
		fmt.Fprintf(&b, "Profile: %s\n", *ord.ProfileID)
	}
	if ord.PublicationURL != nil && *ord.PublicationURL != "" {
		fmt.Fprintf(&b, "Publication URL: %s\n", *ord.PublicationURL)
	}
	if len(ord.CustomData) > 0 {
		b.WriteString("\nForm data:\n")
		keys := make([]string, 0, len(ord.CustomData))
		for k := range ord.CustomData {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := ord.CustomData[k]
			// Strings ficam inline; objetos/listas saem em JSON compacto.
			switch vv := v.(type) {
			case string:
				if vv == "" {
					continue
				}
				fmt.Fprintf(&b, "  %s: %s\n", k, vv)
			default:
				raw, _ := json.Marshal(vv)
				fmt.Fprintf(&b, "  %s: %s\n", k, string(raw))
			}
		}
	}
	return b.String()
}
