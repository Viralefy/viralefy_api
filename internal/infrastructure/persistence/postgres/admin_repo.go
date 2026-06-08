package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type AdminRepo struct{ db *DB }

func NewAdminRepo(db *DB) *AdminRepo { return &AdminRepo{db: db} }

func (r *AdminRepo) GetByEmail(ctx context.Context, email string) (*domain.Admin, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, role, created_at FROM admins WHERE email=$1`, email)
	return scanAdmin(row)
}

func (r *AdminRepo) GetByID(ctx context.Context, id string) (*domain.Admin, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, role, created_at FROM admins WHERE id=$1`, id)
	return scanAdmin(row)
}

func scanAdmin(row pgx.Row) (*domain.Admin, error) {
	var a domain.Admin
	err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Name, &a.Role, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
