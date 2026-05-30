package application

import (
	"context"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

type UserAuthService struct {
	users  domain.UserRepository
	secret []byte
	ttl    time.Duration
}

func NewUserAuthService(users domain.UserRepository, secret string, ttl time.Duration) *UserAuthService {
	return &UserAuthService{users: users, secret: []byte(secret), ttl: ttl}
}

type RegisterInput struct {
	Email    string
	Name     string
	Password string
}

type UserSession struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      UserView  `json:"user"`
}

type UserView struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Instagram string `json:"instagram"`
}

func (s *UserAuthService) Register(ctx context.Context, in RegisterInput) (*UserSession, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Name == "" || len(in.Password) < 8 {
		return nil, domain.ErrInvalidInput
	}
	if existing, _ := s.users.GetByEmail(ctx, in.Email); existing != nil {
		return nil, domain.ErrConflict
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		return nil, err
	}
	u := domain.User{
		ID:           uuid.New().String(),
		Email:        in.Email,
		Name:         in.Name,
		Instagram:    "", // legado — perfis ficam em /v1/me/profiles agora
		PasswordHash: string(hash),
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return s.session(u)
}

func (s *UserAuthService) Login(ctx context.Context, email, password string) (*UserSession, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, domain.ErrUnauthorized
	}
	return s.session(*u)
}

func (s *UserAuthService) session(u domain.User) (*UserSession, error) {
	exp := time.Now().UTC().Add(s.ttl)
	claims := jwt.MapClaims{
		"sub":  u.ID,
		"role": "user",
		"exp":  exp.Unix(),
		"iat":  time.Now().UTC().Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return nil, err
	}
	return &UserSession{
		Token:     signed,
		ExpiresAt: exp,
		User:      UserView{ID: u.ID, Email: u.Email, Name: u.Name, Instagram: u.Instagram},
	}, nil
}

func (s *UserAuthService) ValidateToken(tokenStr string) (userID string, err error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, domain.ErrUnauthorized
		}
		return s.secret, nil
	})
	if err != nil || !t.Valid {
		return "", domain.ErrUnauthorized
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", domain.ErrUnauthorized
	}
	if role, _ := claims["role"].(string); role != "user" {
		return "", domain.ErrUnauthorized
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", domain.ErrUnauthorized
	}
	return sub, nil
}
