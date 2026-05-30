package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type OrderRepo struct{ db *DB }

func NewOrderRepo(db *DB) *OrderRepo { return &OrderRepo{db: db} }

const orderCols = `id, user_id, plan_id, status, amount_cents, currency,
	display_currency, display_amount, settlement_currency, settlement_amount,
	gateway_id, external_ref, payment_url, payment_extra, created_at, updated_at`

func (r *OrderRepo) Create(ctx context.Context, o domain.Order) error {
	extra, _ := json.Marshal(o.PaymentExtra)
	if len(extra) == 0 {
		extra = []byte("{}")
	}
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, plan_id, status, amount_cents, currency,
			display_currency, display_amount, settlement_currency, settlement_amount,
			gateway_id, payment_extra)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		o.ID, o.UserID, o.PlanID, o.Status, o.AmountCents, o.Currency,
		o.DisplayCurrency, o.DisplayAmount, o.SettlementCurrency, o.SettlementAmount,
		o.GatewayID, extra)
	return err
}

func (r *OrderRepo) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	row := r.db.pool.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE id=$1`, id)
	return scanOrder(row)
}

func (r *OrderRepo) ListByUser(ctx context.Context, userID string) ([]domain.Order, error) {
	rows, err := r.db.pool.Query(ctx, `SELECT `+orderCols+`
		FROM orders WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (r *OrderRepo) ListAll(ctx context.Context) ([]domain.Order, error) {
	rows, err := r.db.pool.Query(ctx, `SELECT `+orderCols+`
		FROM orders ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

const orderViewCols = `o.id, o.user_id, o.plan_id, o.status, o.amount_cents, o.currency,
	o.display_currency, o.display_amount, o.settlement_currency, o.settlement_amount,
	o.gateway_id, o.external_ref, o.payment_url, o.payment_extra, o.created_at, o.updated_at,
	COALESCE(p.name, ''), COALESCE(p.category, '')`

func (r *OrderRepo) ListViewByUser(ctx context.Context, userID string) ([]domain.OrderView, error) {
	rows, err := r.db.pool.Query(ctx, `SELECT `+orderViewCols+`
		FROM orders o LEFT JOIN plans p ON p.id = o.plan_id
		WHERE o.user_id=$1 ORDER BY o.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrderViews(rows)
}

func (r *OrderRepo) ListAllView(ctx context.Context) ([]domain.OrderView, error) {
	rows, err := r.db.pool.Query(ctx, `SELECT `+orderViewCols+`
		FROM orders o LEFT JOIN plans p ON p.id = o.plan_id
		ORDER BY o.created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrderViews(rows)
}

func (r *OrderRepo) UpdateStatus(ctx context.Context, id string, status domain.OrderStatus, externalRef *string) error {
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE orders SET status=$2, external_ref=$3, updated_at=NOW() WHERE id=$1`, id, status, externalRef)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *OrderRepo) UpdatePayment(ctx context.Context, id, externalRef, paymentURL string, extra map[string]string) error {
	raw, _ := json.Marshal(extra)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE orders SET external_ref=$2, payment_url=$3, payment_extra=$4, updated_at=NOW()
		WHERE id=$1`, id, nullable(externalRef), nullable(paymentURL), raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func scanOrders(rows pgx.Rows) ([]domain.Order, error) {
	list := []domain.Order{}
	for rows.Next() {
		o, err := scanOrderRow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *o)
	}
	return list, rows.Err()
}

func scanOrder(row pgx.Row) (*domain.Order, error) {
	o, err := scanOrderRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return o, err
}

func scanOrderRow(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	var extra []byte
	err := row.Scan(&o.ID, &o.UserID, &o.PlanID, &o.Status, &o.AmountCents, &o.Currency,
		&o.DisplayCurrency, &o.DisplayAmount, &o.SettlementCurrency, &o.SettlementAmount,
		&o.GatewayID, &o.ExternalRef, &o.PaymentURL, &extra, &o.CreatedAt, &o.UpdatedAt)
	if err == nil {
		o.PaymentExtra = map[string]string{}
		if len(extra) > 0 {
			_ = json.Unmarshal(extra, &o.PaymentExtra)
		}
	}
	return &o, err
}

func scanOrderViews(rows pgx.Rows) ([]domain.OrderView, error) {
	list := []domain.OrderView{}
	for rows.Next() {
		var v domain.OrderView
		var extra []byte
		err := rows.Scan(&v.ID, &v.UserID, &v.PlanID, &v.Status, &v.AmountCents, &v.Currency,
			&v.DisplayCurrency, &v.DisplayAmount, &v.SettlementCurrency, &v.SettlementAmount,
			&v.GatewayID, &v.ExternalRef, &v.PaymentURL, &extra, &v.CreatedAt, &v.UpdatedAt,
			&v.PlanName, &v.PlanCategory)
		if err != nil {
			return nil, err
		}
		v.PaymentExtra = map[string]string{}
		if len(extra) > 0 {
			_ = json.Unmarshal(extra, &v.PaymentExtra)
		}
		list = append(list, v)
	}
	return list, rows.Err()
}
