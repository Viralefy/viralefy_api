package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type OrderRepo struct{ db *DB }

func NewOrderRepo(db *DB) *OrderRepo { return &OrderRepo{db: db} }

func (r *OrderRepo) Create(ctx context.Context, o domain.Order) error {
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, plan_id, status, amount_cents, currency, gateway_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		o.ID, o.UserID, o.PlanID, o.Status, o.AmountCents, o.Currency, o.GatewayID)
	return err
}

func (r *OrderRepo) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, user_id, plan_id, status, amount_cents, currency, gateway_id, external_ref, created_at, updated_at
		FROM orders WHERE id=$1`, id)
	return scanOrder(row)
}

func (r *OrderRepo) ListByUser(ctx context.Context, userID string) ([]domain.Order, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, user_id, plan_id, status, amount_cents, currency, gateway_id, external_ref, created_at, updated_at
		FROM orders WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (r *OrderRepo) ListAll(ctx context.Context) ([]domain.Order, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, user_id, plan_id, status, amount_cents, currency, gateway_id, external_ref, created_at, updated_at
		FROM orders ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
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

func scanOrders(rows pgx.Rows) ([]domain.Order, error) {
	var list []domain.Order
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
	return scanOrderRow(row)
}

func scanOrderRow(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(&o.ID, &o.UserID, &o.PlanID, &o.Status, &o.AmountCents, &o.Currency, &o.GatewayID, &o.ExternalRef, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &o, err
}
