package domain

import "time"

type Admin struct {
	ID           string
	Email        string
	PasswordHash string
	Name         string
	CreatedAt    time.Time
}

type AdminRepository interface {
	GetByEmail(ctx interface{}, email string) (*Admin, error)
}
