package application

import (
	"context"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	admins domain.AdminRepository
	secret []byte
	ttl    time.Duration
}

func NewAuthService(admins domain.AdminRepository, secret string, ttl time.Duration) *AuthService {
	return &AuthService{admins: admins, secret: []byte(secret), ttl: ttl}
}

type LoginInput struct {
	Email    string
	Password string
}

type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	AdminID   string    `json:"admin_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
}

func (s *AuthService) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	admin, err := s.admins.GetByEmail(ctx, email)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(in.Password)) != nil {
		return nil, domain.ErrUnauthorized
	}
	exp := time.Now().UTC().Add(s.ttl)
	claims := jwt.MapClaims{
		"sub":   admin.ID,
		"email": admin.Email,
		"role":  "admin",
		"exp":   exp.Unix(),
		"iat":   time.Now().UTC().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:     signed,
		ExpiresAt: exp,
		AdminID:   admin.ID,
		Email:     admin.Email,
		Name:      admin.Name,
	}, nil
}

func (s *AuthService) ValidateToken(tokenStr string) (adminID string, err error) {
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
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", domain.ErrUnauthorized
	}
	return sub, nil
}
