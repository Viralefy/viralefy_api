package domain

import (
	"context"
	"time"
)

type Admin struct {
	ID           string
	Email        string
	PasswordHash string
	Name         string
	Role         string
	CreatedAt    time.Time
}

type AdminRepository interface {
	GetByEmail(ctx context.Context, email string) (*Admin, error)
	GetByID(ctx context.Context, id string) (*Admin, error)
}
