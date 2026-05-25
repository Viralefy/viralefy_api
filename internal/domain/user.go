package domain

import "time"

type User struct {
	ID           string
	Email        string
	Name         string
	Instagram    string
	PasswordHash string
	CreatedAt    time.Time
}

type UserRepository interface {
	Create(ctx interface{}, u User) error
	GetByEmail(ctx interface{}, email string) (*User, error)
	GetByID(ctx interface{}, id string) (*User, error)
}
