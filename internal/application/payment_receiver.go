package application

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/viralefy/viralefy_api/internal/domain"
)

// PaymentReceiver é o ponto único de entrada para confirmações de pagamento
// (webhook ou ação manual do admin). Idempotente: chamadas repetidas para
// o mesmo external_ref / id já pago são no-op.
type PaymentReceiver struct {
	invoices   domain.InvoiceRepository
	orders     domain.OrderRepository
	invoiceSvc *InvoiceService
}

func NewPaymentReceiver(invoices domain.InvoiceRepository, orders domain.OrderRepository, invoiceSvc *InvoiceService) *PaymentReceiver {
	return &PaymentReceiver{invoices: invoices, orders: orders, invoiceSvc: invoiceSvc}
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
		log.Printf("payment_receiver: invoice %s confirmada via external_ref=%s", inv.ID, ref)
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
		log.Printf("payment_receiver: order %s marcada como paga via external_ref=%s", ord.ID, ref)
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
	return r.orders.UpdateStatus(ctx, orderID, domain.OrderStatusPaid, ord.ExternalRef)
}
