package application

import (
	"context"
	"crypto/rsa"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/viralefy/viralefy_api/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// UserAuthService — mesma estratégia dual-sign do AuthService (Fase 4.1).
type UserAuthService struct {
	users             domain.UserRepository
	RSAPrivKey        *rsa.PrivateKey
	LegacyHS256Secret []byte
	// legacyHS256Disabled — kill-switch (Fase 4.1 follow-up). Espelha o
	// comportamento do AuthService de admin: bloqueia HS256 mesmo com
	// secret presente, pra cutover seguro sem reiniciar com env zerada.
	legacyHS256Disabled bool
	kid                 string
	ttl                 time.Duration
	// referrals opcional. Quando setado, Register chama RecordReferral se
	// tracking[referrer_code] estiver presente. Best-effort.
	referrals *ReferralService
}

// SetReferrals opt-in.
func (s *UserAuthService) SetReferrals(svc *ReferralService) {
	s.referrals = svc
}

// SetLegacyHS256Disabled — ver AuthService.SetLegacyHS256Disabled.
func (s *UserAuthService) SetLegacyHS256Disabled(disabled bool) {
	s.legacyHS256Disabled = disabled
}

func NewUserAuthService(users domain.UserRepository, rsaKey *rsa.PrivateKey, legacyHS256Secret []byte, ttl time.Duration) *UserAuthService {
	return &UserAuthService{
		users:             users,
		RSAPrivKey:        rsaKey,
		LegacyHS256Secret: legacyHS256Secret,
		kid:               deriveKID(rsaKey),
		ttl:               ttl,
	}
}

type RegisterInput struct {
	Email    string
	Name     string
	Password string
	// Tracking first-touch (utm/fbclid/referrer/landing_url enriquecido com
	// IP+UA server-side). Persistido em users.tracking_data.
	Tracking map[string]any
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
		TrackingData: in.Tracking,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	// Referral signup hook — espelha CheckoutService.
	if s.referrals != nil {
		if rc, ok := in.Tracking["referrer_code"].(string); ok && rc != "" {
			_ = s.referrals.RecordReferral(ctx, u.ID, rc)
		}
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
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if s.kid != "" {
		tok.Header["kid"] = s.kid
	}
	signed, err := tok.SignedString(s.RSAPrivKey)
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
	t, perr := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodRSA:
			if s.RSAPrivKey == nil {
				return nil, domain.ErrUnauthorized
			}
			return &s.RSAPrivKey.PublicKey, nil
		case *jwt.SigningMethodHMAC:
			if s.legacyHS256Disabled || len(s.LegacyHS256Secret) == 0 {
				return nil, domain.ErrUnauthorized
			}
			return s.LegacyHS256Secret, nil
		default:
			return nil, domain.ErrUnauthorized
		}
	})
	if perr != nil || !t.Valid {
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
