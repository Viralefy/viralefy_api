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

// ListWithCreditBalance — usado pelo backoffice. LEFT JOIN no credit_accounts
// (saldo 0 quando o usuário ainda não fez recarga).
func (r *UserRepo) ListWithCreditBalance(ctx context.Context, limit int) ([]domain.UserView, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.db.pool.Query(ctx, `
		SELECT u.id, u.email, u.name, u.instagram, u.created_at,
		       COALESCE(c.balance_cents, 0)
		FROM users u
		LEFT JOIN credit_accounts c ON c.user_id = u.id
		ORDER BY u.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []domain.UserView{}
	for rows.Next() {
		var v domain.UserView
		if err := rows.Scan(&v.ID, &v.Email, &v.Name, &v.Instagram, &v.CreatedAt, &v.BalanceCents); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}
