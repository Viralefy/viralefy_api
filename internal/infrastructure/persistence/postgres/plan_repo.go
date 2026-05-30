package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type PlanRepo struct{ db *DB }

func NewPlanRepo(db *DB) *PlanRepo { return &PlanRepo{db: db} }

const planCols = `plans.id, plans.name, plans.description, plans.category, plans.followers_qty, plans.price_cents, plans.currency, plans.active, plans.sort_order, plans.created_at, plans.updated_at,
	COALESCE((SELECT json_object_agg(pp.currency_code, pp.amount) FROM plan_prices pp WHERE pp.plan_id = plans.id), '{}')`

func (r *PlanRepo) ListActive(ctx context.Context) ([]domain.Plan, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT `+planCols+`
		FROM plans WHERE active = true ORDER BY sort_order ASC, followers_qty ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlans(rows)
}

func (r *PlanRepo) ListAll(ctx context.Context) ([]domain.Plan, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT `+planCols+`
		FROM plans ORDER BY sort_order ASC, followers_qty ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlans(rows)
}

func (r *PlanRepo) GetByID(ctx context.Context, id string) (*domain.Plan, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT `+planCols+`
		FROM plans WHERE id = $1`, id)
	p, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return p, err
}

func (r *PlanRepo) Create(ctx context.Context, p domain.Plan) error {
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO plans (id, name, description, category, followers_qty, price_cents, currency, active, sort_order)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, p.Name, p.Description, p.Category, p.FollowersQty, p.PriceCents, p.Currency, p.Active, p.SortOrder)
	return err
}

func (r *PlanRepo) Update(ctx context.Context, p domain.Plan) error {
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE plans SET name=$2, description=$3, category=$4, followers_qty=$5, price_cents=$6, currency=$7, active=$8, sort_order=$9, updated_at=NOW()
		WHERE id=$1`, p.ID, p.Name, p.Description, p.Category, p.FollowersQty, p.PriceCents, p.Currency, p.Active, p.SortOrder)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *PlanRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.db.pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanPlans(rows pgx.Rows) ([]domain.Plan, error) {
	var list []domain.Plan
	for rows.Next() {
		p, err := scanPlanRow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	return list, rows.Err()
}

func scanPlan(row pgx.Row) (*domain.Plan, error) {
	return scanPlanRow(row)
}

func scanPlanRow(row pgx.Row) (*domain.Plan, error) {
	var p domain.Plan
	var pricesRaw []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Category, &p.FollowersQty, &p.PriceCents, &p.Currency, &p.Active, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt, &pricesRaw)
	if err != nil {
		return &p, err
	}
	p.Prices = map[string]string{}
	if len(pricesRaw) > 0 {
		_ = json.Unmarshal(pricesRaw, &p.Prices)
	}
	return &p, nil
}

func (r *PlanRepo) UpsertPrices(ctx context.Context, planID string, prices map[string]string) error {
	for code, amount := range prices {
		if amount == "" {
			continue
		}
		_, err := r.db.pool.Exec(ctx, `
			INSERT INTO plan_prices (plan_id, currency_code, amount)
			VALUES ($1,$2,$3)
			ON CONFLICT (plan_id, currency_code) DO UPDATE SET amount = EXCLUDED.amount`,
			planID, code, amount)
		if err != nil {
			return err
		}
	}
	return nil
}
