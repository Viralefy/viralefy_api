package domain

import (
	"context"
	"time"
)

type User struct {
	ID           string
	Email        string
	Name         string
	Instagram    string
	PasswordHash string
	CreatedAt    time.Time
}

type UserRepository interface {
	Create(ctx context.Context, u User) error
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
}
