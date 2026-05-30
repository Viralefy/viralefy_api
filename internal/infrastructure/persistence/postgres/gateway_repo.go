package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type GatewayRepo struct{ db *DB }

func NewGatewayRepo(db *DB) *GatewayRepo { return &GatewayRepo{db: db} }

func (r *GatewayRepo) ListAll(ctx context.Context) ([]domain.PaymentGateway, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT id, name, provider, active, config, created_at, updated_at FROM payment_gateways ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []domain.PaymentGateway
	for rows.Next() {
		g, err := scanGateway(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *g)
	}
	return list, rows.Err()
}

func (r *GatewayRepo) GetByID(ctx context.Context, id string) (*domain.PaymentGateway, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, name, provider, active, config, created_at, updated_at FROM payment_gateways WHERE id=$1`, id)
	g, err := scanGateway(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return g, err
}

func (r *GatewayRepo) GetDefaultActive(ctx context.Context) (*domain.PaymentGateway, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, name, provider, active, config, created_at, updated_at FROM payment_gateways WHERE active=true ORDER BY created_at LIMIT 1`)
	g, err := scanGateway(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return g, err
}

func (r *GatewayRepo) GetActiveByProvider(ctx context.Context, provider string) (*domain.PaymentGateway, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, name, provider, active, config, created_at, updated_at
		FROM payment_gateways WHERE active=true AND provider=$1 ORDER BY created_at LIMIT 1`, provider)
	g, err := scanGateway(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return g, err
}

func (r *GatewayRepo) Create(ctx context.Context, g domain.PaymentGateway) error {
	cfg, _ := json.Marshal(g.Config)
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO payment_gateways (id, name, provider, active, config) VALUES ($1,$2,$3,$4,$5)`,
		g.ID, g.Name, g.Provider, g.Active, cfg)
	return err
}

func (r *GatewayRepo) Update(ctx context.Context, g domain.PaymentGateway) error {
	cfg, _ := json.Marshal(g.Config)
	tag, err := r.db.pool.Exec(ctx, `
		UPDATE payment_gateways SET name=$2, provider=$3, active=$4, config=$5, updated_at=NOW() WHERE id=$1`,
		g.ID, g.Name, g.Provider, g.Active, cfg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *GatewayRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.db.pool.Exec(ctx, `DELETE FROM payment_gateways WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanGateway(row pgx.Row) (*domain.PaymentGateway, error) {
	var g domain.PaymentGateway
	var raw []byte
	err := row.Scan(&g.ID, &g.Name, &g.Provider, &g.Active, &raw, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	g.Config = map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &g.Config)
	}
	return &g, nil
}
