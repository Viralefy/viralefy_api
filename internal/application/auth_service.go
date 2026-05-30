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
	roles  domain.RoleRepository
	secret []byte
	ttl    time.Duration
}

func NewAuthService(admins domain.AdminRepository, roles domain.RoleRepository, secret string, ttl time.Duration) *AuthService {
	return &AuthService{admins: admins, roles: roles, secret: []byte(secret), ttl: ttl}
}

type LoginInput struct {
	Email    string
	Password string
}

type LoginResult struct {
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expires_at"`
	AdminID     string    `json:"admin_id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions"`
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
	perms, err := s.roles.GetPermissions(ctx, admin.Role)
	if err != nil {
		return nil, err
	}
	exp := time.Now().UTC().Add(s.ttl)
	claims := jwt.MapClaims{
		"sub":   admin.ID,
		"typ":   "admin",
		"role":  admin.Role,
		"email": admin.Email,
		"exp":   exp.Unix(),
		"iat":   time.Now().UTC().Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:       signed,
		ExpiresAt:   exp,
		AdminID:     admin.ID,
		Email:       admin.Email,
		Name:        admin.Name,
		Role:        admin.Role,
		Permissions: perms,
	}, nil
}

// ValidateAdmin valida o token de admin e monta o Principal (com permissões
// carregadas do papel — sempre frescas, não confia em perms embutidas no JWT).
func (s *AuthService) ValidateAdmin(ctx context.Context, tokenStr string) (domain.Principal, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, domain.ErrUnauthorized
		}
		return s.secret, nil
	})
	if err != nil || !t.Valid {
		return domain.Principal{}, domain.ErrUnauthorized
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return domain.Principal{}, domain.ErrUnauthorized
	}
	if typ, _ := claims["typ"].(string); typ != "admin" {
		return domain.Principal{}, domain.ErrUnauthorized
	}
	sub, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	if sub == "" || role == "" {
		return domain.Principal{}, domain.ErrUnauthorized
	}
	perms, err := s.roles.GetPermissions(ctx, role)
	if err != nil {
		return domain.Principal{}, domain.ErrUnauthorized
	}
	return domain.Principal{AdminID: sub, Role: role, Permissions: perms}, nil
}

// CurrentPrincipal recarrega o principal a partir do papel (uso em /admin/me).
func (s *AuthService) Roles(ctx context.Context) ([]domain.Role, error) {
	return s.roles.List(ctx)
}
