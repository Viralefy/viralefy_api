package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type ctxKey string

const adminIDKey ctxKey = "admin_id"

func AdminAuth(auth *application.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				writeError(w, domain.ErrUnauthorized)
				return
			}
			token := strings.TrimPrefix(h, "Bearer ")
			id, err := auth.ValidateToken(token)
			if err != nil {
				writeError(w, err)
				return
			}
			ctx := context.WithValue(r.Context(), adminIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
