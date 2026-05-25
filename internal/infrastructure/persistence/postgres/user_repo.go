package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type UserRepo struct{ db *DB }

func NewUserRepo(db *DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u domain.User) error {
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO users (id, email, name, instagram, password_hash)
		VALUES ($1,$2,$3,$4,$5)`, u.ID, u.Email, u.Name, u.Instagram, u.PasswordHash)
	return err
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, email, name, instagram, password_hash, created_at FROM users WHERE email=$1`, email)
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Instagram, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &u, err
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	row := r.db.pool.QueryRow(ctx, `
		SELECT id, email, name, instagram, password_hash, created_at FROM users WHERE id=$1`, id)
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Instagram, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &u, err
}
