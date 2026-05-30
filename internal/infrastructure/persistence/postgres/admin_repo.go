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
	var a domain.Admin
	err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Name, &a.Role, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &a, err
}
