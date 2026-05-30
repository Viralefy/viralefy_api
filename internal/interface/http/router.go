package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/viralefy/viralefy_api/internal/domain"
)

func NewRouter(h *Handlers, corsOrigins []string, adminAuth, userAuth, optionalUserAuth func(http.Handler) http.Handler) http.Handler {
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
		// Público
		r.Get("/plans", h.ListPublicPlans)
		r.Get("/categories", h.ListCategories)
		r.Get("/currencies", h.ListCurrencies)
		// Checkout aceita token opcional — quando logado, usa profile_id e
		// pode pagar com créditos. Sem token, cria conta na hora.
		r.With(optionalUserAuth).Post("/checkout", h.CreateCheckout)

		// Webhooks dos providers (sem auth — assinatura é validada no handler).
		r.Post("/webhooks/woovi", h.WooviWebhook)
		r.Post("/webhooks/heleket", h.HeleketWebhook)

		// Auth admin (backoffice)
		r.Post("/auth/login", h.AdminLogin)

		// Auth de usuário (loja)
		r.Post("/auth/user/register", h.UserRegister)
		r.Post("/auth/user/login", h.UserLogin)

		// Área logada do usuário
		r.Route("/me", func(r chi.Router) {
			r.Use(userAuth)
			r.Get("/orders", h.MeOrders)

			r.Get("/profiles", h.MeListProfiles)
			r.Post("/profiles", h.MeAddProfile)
			r.Delete("/profiles/{id}", h.MeDeleteProfile)

			r.Get("/credits", h.MeCredits)
			r.Get("/transactions", h.MeTransactions)
			r.Post("/recharge", h.MeRecharge)
			r.Get("/invoices", h.MeListInvoices)

			r.Get("/tickets", h.MeListTickets)
			r.Post("/tickets", h.MeCreateTicket)
			r.Get("/tickets/{id}", h.MeGetTicket)
			r.Post("/tickets/{id}/messages", h.MeReplyTicket)
		})

		// Admin — RBAC: cada rota exige uma permissão (após AdminAuth).
		r.Route("/admin", func(r chi.Router) {
			r.Use(adminAuth)

			r.Get("/me", h.AdminMe)
			r.With(RequirePermission(domain.PermAdminsManage)).Get("/roles", h.AdminListRoles)

			r.With(RequirePermission(domain.PermPlansRead)).Get("/plans", h.AdminListPlans)
			r.With(RequirePermission(domain.PermPlansWrite)).Post("/plans", h.AdminCreatePlan)
			r.With(RequirePermission(domain.PermPlansWrite)).Put("/plans/{id}", h.AdminUpdatePlan)
			r.With(RequirePermission(domain.PermPlansWrite)).Delete("/plans/{id}", h.AdminDeletePlan)

			r.With(RequirePermission(domain.PermGatewaysRead)).Get("/gateways", h.AdminListGateways)
			r.With(RequirePermission(domain.PermGatewaysWrite)).Post("/gateways", h.AdminCreateGateway)
			r.With(RequirePermission(domain.PermGatewaysWrite)).Put("/gateways/{id}", h.AdminUpdateGateway)
			r.With(RequirePermission(domain.PermGatewaysWrite)).Delete("/gateways/{id}", h.AdminDeleteGateway)

			r.With(RequirePermission(domain.PermOrdersRead)).Get("/orders", h.AdminListOrders)

			r.With(RequirePermission(domain.PermCurrenciesRead)).Get("/currencies", h.AdminListCurrencies)
			r.With(RequirePermission(domain.PermCurrenciesWrite)).Put("/currencies/{code}", h.AdminUpdateCurrency)

			r.With(RequirePermission(domain.PermTicketsRead)).Get("/tickets", h.AdminListTickets)
			r.With(RequirePermission(domain.PermTicketsRead)).Get("/tickets/{id}", h.AdminGetTicket)
			r.With(RequirePermission(domain.PermTicketsWrite)).Post("/tickets/{id}/messages", h.AdminReplyTicket)
			r.With(RequirePermission(domain.PermTicketsWrite)).Patch("/tickets/{id}", h.AdminUpdateTicket)

			// Invoices (recargas). Marcar como paga é sensível → admins:manage.
			r.With(RequirePermission(domain.PermOrdersRead)).Get("/invoices", h.AdminListInvoices)
			r.With(RequirePermission(domain.PermAdminsManage)).Post("/invoices/{id}/mark-paid", h.AdminMarkInvoicePaid)

			// Usuários, ajuste de saldo e marcação manual de pedido.
			r.With(RequirePermission(domain.PermOrdersRead)).Get("/users", h.AdminListUsers)
			r.With(RequirePermission(domain.PermOrdersRead)).Get("/users/{id}", h.AdminGetUser)
			r.With(RequirePermission(domain.PermAdminsManage)).Post("/users/{id}/credits/adjust", h.AdminAdjustCredits)
			r.With(RequirePermission(domain.PermAdminsManage)).Post("/orders/{id}/mark-paid", h.AdminMarkOrderPaid)
		})
	})

	return r
}
