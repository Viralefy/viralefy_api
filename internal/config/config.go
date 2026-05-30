package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port         string
	BindHost     string
	DatabaseURL  string
	JWTSecret    string
	JWTTTL       time.Duration
	CORSOrigins  []string
	SMTPAddr     string
	SMTPUser     string
	SMTPPass     string
	SMTPFrom     string
	SMTPFromName string

	EmailProvider  string
	ResendAPIKey   string
	ResendFrom     string
	ResendFromName string
	ResendBaseURL  string

	SiteURL string // URL pública da loja (https://viralefy.com) — usada em e-mails
}

func Load() (Config, error) {
	port := getenv("PORT", "8080")
	// Default seguro: só localhost. Production fica atrás do Caddy. Para expor
	// externamente sem proxy, defina BIND_HOST=0.0.0.0 explicitamente.
	bindHost := getenv("BIND_HOST", "127.0.0.1")
	db := getenv("DATABASE_URL", "postgres://viralefy:viralefy@localhost:5432/viralefy?sslmode=disable")
	secret := getenv("JWT_SECRET", "change-me-in-production-min-32-chars!!")
	ttlHours, _ := strconv.Atoi(getenv("JWT_TTL_HOURS", "24"))
	cors := getenv("CORS_ORIGINS", "http://localhost:3000,http://localhost:3001")
	cfg := Config{
		Port:         port,
		BindHost:     bindHost,
		DatabaseURL:  db,
		JWTSecret:    secret,
		JWTTTL:       time.Duration(ttlHours) * time.Hour,
		CORSOrigins:  splitCSV(cors),
		SMTPAddr:     getenv("SMTP_ADDR", ""),
		SMTPUser:     getenv("SMTP_USER", ""),
		SMTPPass:     getenv("SMTP_PASS", ""),
		SMTPFrom:     getenv("SMTP_FROM", "no-reply@viralefy.local"),
		SMTPFromName: getenv("SMTP_FROM_NAME", "Viralefy"),

		EmailProvider:  getenv("EMAIL_PROVIDER", ""),
		ResendAPIKey:   getenv("RESEND_API_KEY", ""),
		ResendFrom:     getenv("RESEND_FROM", "onboarding@resend.dev"),
		ResendFromName: getenv("RESEND_FROM_NAME", "Viralefy"),
		ResendBaseURL:  getenv("RESEND_BASE_URL", "https://api.resend.com"),

		SiteURL: getenv("SITE_URL", getenv("NEXT_PUBLIC_SITE_URL", "https://viralefy.com")),
	}
	if len(cfg.JWTSecret) < 16 {
		return cfg, fmt.Errorf("JWT_SECRET must be at least 16 characters")
	}
	return cfg, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range split(s, ',') {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func split(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, trim(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, trim(s[start:]))
	return parts
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
