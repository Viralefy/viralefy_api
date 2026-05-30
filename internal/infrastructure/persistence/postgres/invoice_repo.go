package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type InvoiceRepo struct{ db *DB }

func NewInvoiceRepo(db *DB) *InvoiceRepo { return &InvoiceRepo{db: db} }

const invoiceCols = `id, user_id, amount_cents, currency,
	display_currency, display_amount, settlement_currency, settlement_amount,
	status, gateway_id, external_ref, payment_url, payment_extra,
	created_at, updated_at, paid_at`

func (r *InvoiceRepo) Create(ctx context.Context, inv domain.Invoice) error {
	extra, _ := json.Marshal(inv.PaymentExtra)
	if len(extra) == 0 {
		extra = []byte("{}")
	}
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO invoices (id, user_id, amount_cents, currency,
			display_currency, display_amount, settlement_currency, settlement_amount,
			status, gateway_id, payment_extra)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		inv.ID, inv.UserID, inv.AmountCents, inv.Currency,
		inv.DisplayCurrency, inv.DisplayAmount, inv.SettlementCurrency, inv.SettlementAmount,
		inv.Status, inv.GatewayID, extra)
	return err
}

func (r *InvoiceRepo) GetByID(ctx context.Context, id string) (*domain.Invoice, error) {
	row := r.db.pool.QueryRow(ctx, `SELECT `+invoiceCols+` FROM invoices WHERE id=$1`, id)
	return scanInvoice(row)
}

func (r *InvoiceRepo) GetByExternalRef(ctx context.Context, ref string) (*domain.Invoice, error) {
	row := r.db.pool.QueryRow(ctx, `SELECT `+invoiceCols+` FROM invoices WHERE external_ref=$1 LIMIT 1`, ref)
	return scanInvoice(row)
}

func (r *InvoiceRepo) ListByUser(ctx context.Context, userID string) ([]domain.Invoice, error) {
	rows, err := r.db.pool.Query(ctx, `SELECT `+invoiceCols+`
		FROM invoices WHERE user_id=$1 ORDER BY created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInvoices(rows)
}

func (r *InvoiceRepo) ListAll(ctx context.Context, statusFilter string) ([]domain.Invoice, error) {
	var rows pgx.Rows
	var err error
	if statusFilter != "" {
		rows, err = r.db.pool.Query(ctx, `SELECT `+invoiceCols+`
			FROM invoices WHERE status=$1 ORDER BY created_at DESC LIMIT 500`, statusFilter)
	} else {
		rows, err = r.db.pool.Query(ctx, `SELECT `+invoiceCols+`
			FROM invoices ORDER BY created_at DESC LIMIT 500`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInvoices(rows)
}

func (r *InvoiceRepo) UpdatePayment(ctx context.Context, id, externalRef, paymentURL string, extra map[string]string) error {
	raw, _ := json.Marshal(extra)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE invoices SET external_ref=$2, payment_url=$3, payment_extra=$4, updated_at=NOW()
		WHERE id=$1`, id, nullable(externalRef), nullable(paymentURL), raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *InvoiceRepo) MarkPaid(ctx context.Context, id string) error {
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE invoices SET status='paid', paid_at=NOW(), updated_at=NOW() WHERE id=$1 AND status != 'paid'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *InvoiceRepo) UpdateStatus(ctx context.Context, id string, status domain.InvoiceStatus) error {
	tag, err := r.db.pool.Exec(ctx, `UPDATE invoices SET status=$2, updated_at=NOW() WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanInvoices(rows pgx.Rows) ([]domain.Invoice, error) {
	list := []domain.Invoice{}
	for rows.Next() {
		inv, err := scanInvoiceRow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *inv)
	}
	return list, rows.Err()
}

func scanInvoice(row pgx.Row) (*domain.Invoice, error) {
	inv, err := scanInvoiceRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return inv, err
}

func scanInvoiceRow(row pgx.Row) (*domain.Invoice, error) {
	var inv domain.Invoice
	var extra []byte
	err := row.Scan(&inv.ID, &inv.UserID, &inv.AmountCents, &inv.Currency,
		&inv.DisplayCurrency, &inv.DisplayAmount, &inv.SettlementCurrency, &inv.SettlementAmount,
		&inv.Status, &inv.GatewayID, &inv.ExternalRef, &inv.PaymentURL, &extra,
		&inv.CreatedAt, &inv.UpdatedAt, &inv.PaidAt)
	if err != nil {
		return &inv, err
	}
	inv.PaymentExtra = map[string]string{}
	if len(extra) > 0 {
		_ = json.Unmarshal(extra, &inv.PaymentExtra)
	}
	return &inv, nil
}
