package application

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/viralefy/viralefy_api/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// AuthService autentica admins. Fase 4.1 dual-sign:
//   - Login() assina com RS256 (RSAPrivKey) e seta `kid`.
//   - ValidateAdmin() tenta RS256 primeiro; falhando, faz fallback pra
//     HS256 com LegacyHS256Secret pra aceitar tokens emitidos antes da
//     migração (janela de 7 dias). Após o cutover, basta zerar
//     LegacyHS256Secret no wire-up pra hard-disable HS256.
type AuthService struct {
	admins            domain.AdminRepository
	roles             domain.RoleRepository
	RSAPrivKey        *rsa.PrivateKey
	LegacyHS256Secret []byte
	kid               string
	ttl               time.Duration
}

func NewAuthService(admins domain.AdminRepository, roles domain.RoleRepository, rsaKey *rsa.PrivateKey, legacyHS256Secret []byte, ttl time.Duration) *AuthService {
	return &AuthService{
		admins:            admins,
		roles:             roles,
		RSAPrivKey:        rsaKey,
		LegacyHS256Secret: legacyHS256Secret,
		kid:               deriveKID(rsaKey),
		ttl:               ttl,
	}
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
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if s.kid != "" {
		tok.Header["kid"] = s.kid
	}
	signed, err := tok.SignedString(s.RSAPrivKey)
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
//
// Dual-sign: tenta RS256 primeiro (token novo). Se o header indicar HS256
// e LegacyHS256Secret estiver configurado, aceita como legado.
func (s *AuthService) ValidateAdmin(ctx context.Context, tokenStr string) (domain.Principal, error) {
	claims, err := s.parseDualSign(tokenStr)
	if err != nil {
		return domain.Principal{}, err
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

// parseDualSign aceita tanto RS256 (atual) quanto HS256 (legado) durante
// a janela de transição. O keyfunc inspeciona t.Method e devolve a chave
// apropriada; falhas implícitas (alg "none", chave faltando, etc.)
// caem em ErrUnauthorized.
func (s *AuthService) parseDualSign(tokenStr string) (jwt.MapClaims, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodRSA:
			if s.RSAPrivKey == nil {
				return nil, domain.ErrUnauthorized
			}
			return &s.RSAPrivKey.PublicKey, nil
		case *jwt.SigningMethodHMAC:
			if len(s.LegacyHS256Secret) == 0 {
				return nil, domain.ErrUnauthorized
			}
			return s.LegacyHS256Secret, nil
		default:
			return nil, domain.ErrUnauthorized
		}
	})
	if err != nil || !t.Valid {
		return nil, domain.ErrUnauthorized
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return nil, domain.ErrUnauthorized
	}
	return claims, nil
}

// CurrentPrincipal recarrega o principal a partir do papel (uso em /admin/me).
func (s *AuthService) Roles(ctx context.Context) ([]domain.Role, error) {
	return s.roles.List(ctx)
}

func deriveKID(priv *rsa.PrivateKey) string {
	if priv == nil {
		return ""
	}
	sum := sha256.Sum256(priv.PublicKey.N.Bytes())
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}
