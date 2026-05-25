package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func NewRouter(h *Handlers, corsOrigins []string, adminAuth func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", Health)
	r.Get("/ready", Ready)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/plans", h.ListPublicPlans)
		r.Post("/checkout", h.Checkout)
		r.Post("/auth/login", h.AdminLogin)

		r.Route("/admin", func(r chi.Router) {
			r.Use(adminAuth)
			r.Get("/plans", h.AdminListPlans)
			r.Post("/plans", h.AdminCreatePlan)
			r.Put("/plans/{id}", h.AdminUpdatePlan)
			r.Delete("/plans/{id}", h.AdminDeletePlan)
			r.Get("/gateways", h.AdminListGateways)
			r.Post("/gateways", h.AdminCreateGateway)
			r.Put("/gateways/{id}", h.AdminUpdateGateway)
			r.Delete("/gateways/{id}", h.AdminDeleteGateway)
			r.Get("/orders", h.AdminListOrders)
		})
	})

	return r
}
