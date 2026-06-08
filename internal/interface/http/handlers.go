package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/domain"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/email"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/jwtkeys"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/payment"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/turnstile"
	"github.com/viralefy/viralefy_api/internal/infrastructure/observability"
	"github.com/viralefy/viralefy_api/internal/infrastructure/persistence/postgres"
)

type Handlers struct {
	Plans           *application.PlanService
	Checkout        *application.CheckoutService
	Gateways        *application.GatewayService
	Auth            *application.AuthService
	UserAuth        *application.UserAuthService
	Currencies      *application.CurrencyService
	Categories      domain.CategoryRepository
	Orders          domain.OrderRepository
	Users           domain.UserRepository
	Tickets         *application.TicketService
	Profiles        *application.ProfileService
	Credits         *application.CreditService
	Invoices        *application.InvoiceService
	PaymentReceiver *application.PaymentReceiver
	Turnstile       *turnstile.Service
	Audit           *application.AuditService
	// DB é exposto pra middleware de idempotency (lê/escreve em
	// idempotency_keys). Quase nenhum handler precisa, mas o pattern de
	// passar via Handlers mantém os middlewares chainable.
	DB         *postgres.DB
	Metrics    *application.MetricCaptureService
	Reviews    *application.ReviewService
	EmailRepu  *application.EmailReputationService
	Coupons    *application.CouponService
	OrderSvc   *application.OrderService
	Notifs     *application.UserNotifService
	UserData   *application.UserDataService
	CountryPPP domain.CountryPPPRepository
	Referrals     *application.ReferralService
	ABTests       *application.ABTestService
	Fraud         *application.FraudService
	Refunds       *application.RefundService
	Subscriptions *application.SubscriptionService
	TaxRates      domain.TaxRateRepository
	Tax           *application.TaxService
	WhatsApp      *application.WhatsAppService
	Vendors       *application.VendorService
	APIKeys       *application.APIKeyService
	Events        *application.UserEventService
}

// clientIP extrai o IP do cliente do request, respeitando X-Forwarded-For
// quando vier do Caddy/Cloudflare. Usado pelo Turnstile pra reforçar
// detecção de bot via origem.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Pega o primeiro IP (o cliente original) — o resto são proxies.
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if ra := r.RemoteAddr; ra != "" {
		// "host:port" → "host"
		if idx := strings.LastIndexByte(ra, ':'); idx > 0 {
			return ra[:idx]
		}
		return ra
	}
	return ""
}

// --- Público ---

func (h *Handlers) ListPublicPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := h.Plans.ListPublic(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	// Enriquece com aggregateRating por plano — front emite no JSON-LD do
	// Product e do AggregateOffer. N+1 queries é aceitável aqui: catálogo
	// total < 100 planos e essa rota é cacheada (s-maxage no Caddy).
	if h.Reviews != nil {
		for i := range plans {
			if agg, err := h.Reviews.AggregateByPlan(r.Context(), plans[i].ID); err == nil && agg != nil {
				plans[i].AggregateRating = agg
			}
		}
	}
	writeData(w, http.StatusOK, plans)
}

func (h *Handlers) ListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.Categories.ListActive(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, cats)
}

func (h *Handlers) ListCurrencies(w http.ResponseWriter, r *http.Request) {
	curs, err := h.Currencies.ListDisplayable(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, curs)
}

// CreateRecoveryRequest é o entrypoint para o formulário de Account Recovery
// nas LPs por país. Valida Turnstile, encontra o plano de recuperação, e
// dispara o Checkout com o snapshot completo do form em CustomData.
//
// O ticket é aberto automaticamente após a confirmação do pagamento (hook
// no PaymentReceiver). Pré-pagamento, fica só a order pending.
func (h *Handlers) CreateRecoveryRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Handle             string         `json:"handle"`               // @handle alvo
		Platform           string         `json:"platform"`             // instagram | tiktok
		BanDate            string         `json:"ban_date"`             // ISO 8601 ou texto livre
		EstimatedReason    string         `json:"estimated_reason"`     // suspeita do usuário
		LastPublicationURL string         `json:"last_publication_url"` // último post visível
		Description        string         `json:"description"`          // contexto extra
		ContactEmail       string         `json:"contact_email"`
		ContactName        string         `json:"contact_name"`
		DisplayCurrency    string         `json:"display_currency"`
		PaymentMethod      string         `json:"payment_method,omitempty"`
		TurnstileToken     string         `json:"turnstile_token"`
		Tracking           map[string]any `json:"tracking,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if body.Handle == "" || body.ContactEmail == "" {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if h.Turnstile != nil {
		if err := h.Turnstile.Verify(r.Context(), body.TurnstileToken, clientIP(r)); err != nil {
			observability.FromContext(r.Context()).Warn("recovery: turnstile failed",
				"ip", clientIP(r),
				"error", err.Error(),
			)
			writeError(w, domain.ErrInvalidInput)
			return
		}
	}

	// Encontra o plano de recuperação na categoria dedicada.
	plans, err := h.Plans.ListByCategory(r.Context(), "recuperacao_perfil")
	if err != nil || len(plans) == 0 {
		writeError(w, domain.ErrNotFound)
		return
	}
	plan := plans[0]

	custom := map[string]any{
		"handle":               body.Handle,
		"platform":             body.Platform,
		"ban_date":             body.BanDate,
		"estimated_reason":     body.EstimatedReason,
		"last_publication_url": body.LastPublicationURL,
		"description":          body.Description,
		"contact_email":        body.ContactEmail,
		"contact_name":         body.ContactName,
		"form_type":            "account_recovery",
	}

	in := application.CheckoutInput{
		PlanID:          plan.ID,
		Email:           body.ContactEmail,
		Name:            body.ContactName,
		DisplayCurrency: body.DisplayCurrency,
		PublicationURL:  body.LastPublicationURL,
		PaymentMethod:   body.PaymentMethod,
		CustomData:      custom,
		Tracking:        h.enrichTracking(r, body.Tracking),
	}
	if uid := userIDFromContext(r.Context()); uid != "" {
		in.UserID = uid
	}
	res, err := h.Checkout.Checkout(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

func (h *Handlers) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlanID          string                        `json:"plan_id"`
		Email           string                        `json:"email"`
		Name            string                        `json:"name"`
		DisplayCurrency string                        `json:"display_currency"`
		ProfileID       string                        `json:"profile_id,omitempty"`
		NewProfile      *application.NewProfileInline `json:"new_profile,omitempty"`
		PublicationURL  string                        `json:"publication_url,omitempty"`
		PaymentMethod   string                        `json:"payment_method,omitempty"` // gateway | credits
		CustomData      map[string]any                `json:"custom_data,omitempty"`
		Tracking        map[string]any                `json:"tracking,omitempty"`
		CouponCode      string                        `json:"coupon_code,omitempty"`
		Country         string                        `json:"country,omitempty"`        // país do COMPRADOR (VAT)
		TargetCountry   string                        `json:"target_country,omitempty"` // mercado da entrega (LP)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	in := application.CheckoutInput{
		PlanID:          body.PlanID,
		Email:           body.Email,
		Name:            body.Name,
		DisplayCurrency: body.DisplayCurrency,
		ProfileID:       body.ProfileID,
		NewProfile:      body.NewProfile,
		PublicationURL:  body.PublicationURL,
		PaymentMethod:   body.PaymentMethod,
		CustomData:      body.CustomData,
		Tracking:        h.enrichTracking(r, body.Tracking),
		CouponCode:      body.CouponCode,
		Country:         body.Country,
		TargetCountry:   body.TargetCountry,
	}
	// Se houver token de usuário, força o userID do token (rota /v1/checkout é
	// pública mas honra a autenticação opcional para credit/profile linkage).
	if uid := userIDFromContext(r.Context()); uid != "" {
		in.UserID = uid
	}
	res, err := h.Checkout.Checkout(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

// --- Auth de usuário ---

func (h *Handlers) UserRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email          string         `json:"email"`
		Name           string         `json:"name"`
		Password       string         `json:"password"`
		TurnstileToken string         `json:"turnstile_token"`
		Tracking       map[string]any `json:"tracking,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if !h.verifyTurnstile(r, body.TurnstileToken) {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	res, err := h.UserAuth.Register(r.Context(), application.RegisterInput{
		Email: body.Email, Name: body.Name, Password: body.Password,
		Tracking: h.enrichTracking(r, body.Tracking),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

func (h *Handlers) UserLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email          string `json:"email"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if !h.verifyTurnstile(r, body.TurnstileToken) {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	res, err := h.UserAuth.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

// enrichTracking pega o snapshot client-side (utm/fbclid/gclid/referrer/
// landing_url/client_id/etc.) e adiciona campos server-side que o cliente
// não consegue forjar: IP real, user-agent visto pela API, e timestamp
// de submit. Cliente vazio também é OK — voltamos só com server-side.
//
// Tudo num único map[string]any pra ir direto pra orders.tracking jsonb.
func (h *Handlers) enrichTracking(r *http.Request, client map[string]any) map[string]any {
	out := make(map[string]any, len(client)+4)
	for k, v := range client {
		out[k] = v
	}
	out["server_ip"] = clientIP(r)
	if ua := r.Header.Get("User-Agent"); ua != "" {
		out["server_user_agent"] = ua
	}
	if al := r.Header.Get("Accept-Language"); al != "" {
		out["server_accept_language"] = al
	}
	out["server_submitted_at"] = time.Now().UTC().Format(time.RFC3339)
	return out
}

// verifyTurnstile valida o token contra o Cloudflare Turnstile. Quando o
// serviço está desabilitado (TURNSTILE_SECRET_KEY vazio em HML), aceita
// qualquer token (no-op). Retorna true se passou.
func (h *Handlers) verifyTurnstile(r *http.Request, token string) bool {
	if h.Turnstile == nil || !h.Turnstile.Enabled() {
		return true
	}
	if err := h.Turnstile.Verify(r.Context(), token, clientIP(r)); err != nil {
		observability.FromContext(r.Context()).Warn("turnstile failed on auth",
			"path", r.URL.Path,
			"ip", clientIP(r),
			"error", err.Error(),
		)
		return false
	}
	return true
}

// --- Tickets do usuário (loja) --- //

func (h *Handlers) MeListTickets(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	list, err := h.Tickets.ListForUser(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

// MeOpenTicketsCount alimenta o badge "💬 (N)" do Header. Conta tickets
// em status open ou pending (que exigem ação do user ou do suporte).
func (h *Handlers) MeOpenTicketsCount(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	n, err := h.Tickets.CountOpenForUser(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]int{"open": n})
}

func (h *Handlers) MeCreateTicket(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	var body struct {
		Subject string  `json:"subject"`
		Body    string  `json:"body"`
		OrderID *string `json:"order_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	t, err := h.Tickets.Open(r.Context(), application.OpenTicketInput{
		UserID: userID, Subject: body.Subject, Body: body.Body, OrderID: body.OrderID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, t)
}

func (h *Handlers) MeGetTicket(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	d, err := h.Tickets.GetForUser(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, d)
}

func (h *Handlers) MeReplyTicket(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if err := h.Tickets.ReplyAsUser(r.Context(), chi.URLParam(r, "id"), userID, body.Body); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Tickets (admin) --- //

func (h *Handlers) AdminListTickets(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := h.Tickets.AdminList(r.Context(), status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

func (h *Handlers) AdminGetTicket(w http.ResponseWriter, r *http.Request) {
	d, err := h.Tickets.AdminGet(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, d)
}

func (h *Handlers) AdminReplyTicket(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFromContext(r.Context())
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if err := h.Tickets.ReplyAsAdmin(r.Context(), chi.URLParam(r, "id"), p.AdminID, body.Body); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) AdminUpdateTicket(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Status   *string `json:"status,omitempty"`
		Priority *string `json:"priority,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if body.Status != nil {
		if err := h.Tickets.AdminUpdateStatus(r.Context(), id, domain.TicketStatus(*body.Status)); err != nil {
			writeError(w, err)
			return
		}
	}
	if body.Priority != nil {
		if err := h.Tickets.AdminUpdatePriority(r.Context(), id, domain.TicketPriority(*body.Priority)); err != nil {
			writeError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) MeOrders(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	orders, err := h.Orders.ListViewByUser(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, orders)
}

// --- Admin ---

// AdminMe devolve o principal autenticado (papel + permissões) para o
// backoffice adaptar a UI (esconder ações sem permissão).
func (h *Handlers) AdminMe(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	writeData(w, http.StatusOK, p)
}

// AdminBecomeCustomer cria (se necessário) um user record com o mesmo
// email/name do admin logado e devolve uma UserSession. Permite ao admin
// abrir o lado de customer sem precisar de outro registro/login.
//
// Não é login impersonation — é provisionamento idempotente de um shadow
// account paralelo. O password gerado (apenas no PRIMEIRO chamado) é
// devolvido junto pro admin guardar se quiser usar /login normalmente
// depois.
func (h *Handlers) AdminBecomeCustomer(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	adminRow, err := h.Auth.GetAdminByID(r.Context(), p.AdminID)
	if err != nil || adminRow == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	sess, generatedPwd, err := h.UserAuth.EnsureShadowAccount(r.Context(), adminRow.Email, adminRow.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{
		"session":            sess,
		"generated_password": generatedPwd, // vazio se user já existia
	})
}

func (h *Handlers) AdminListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.Auth.Roles(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, roles)
}

func (h *Handlers) AdminLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email          string `json:"email"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if !h.verifyTurnstile(r, body.TurnstileToken) {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	res, err := h.Auth.Login(r.Context(), application.LoginInput{Email: body.Email, Password: body.Password})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

func (h *Handlers) AdminListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := h.Plans.ListAdmin(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plans)
}

func (h *Handlers) AdminCreatePlan(w http.ResponseWriter, r *http.Request) {
	var body application.CreatePlanInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	p, err := h.Plans.Create(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "create", "plan", p.ID, nil, p)
	writeData(w, http.StatusCreated, p)
}

func (h *Handlers) AdminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body application.UpdatePlanInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	body.ID = id
	// Snapshot do plano antes do update (best-effort) — usado no diff.
	before, _ := h.Plans.GetByID(r.Context(), id)
	p, err := h.Plans.Update(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "update", "plan", p.ID, before, p)
	writeData(w, http.StatusOK, p)
}

func (h *Handlers) AdminDeletePlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	before, _ := h.Plans.GetByID(r.Context(), id)
	if err := h.Plans.Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "delete", "plan", id, before, nil)
	w.WriteHeader(http.StatusNoContent)
}

// logAudit é um wrapper enxuto que recolhe actor (do contexto) + meta
// (IP, user-agent) e dispara o AuditService de forma não-bloqueante. Se
// AuditService não estiver configurado (HML sem migration), vira no-op.
func (h *Handlers) logAudit(r *http.Request, action, targetType, targetID string, before, after any) {
	if h.Audit == nil {
		return
	}
	actorType := "system"
	actorID := "system"
	if p, ok := principalFromContext(r.Context()); ok && p.AdminID != "" {
		actorType = "admin"
		actorID = p.AdminID
	}
	h.Audit.Log(r.Context(), application.AuditEntry{
		ActorType:  actorType,
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Before:     before,
		After:      after,
		Metadata: map[string]any{
			"ip":         clientIP(r),
			"user_agent": r.Header.Get("User-Agent"),
			"path":       r.URL.Path,
			"method":     r.Method,
		},
	})
}

func (h *Handlers) AdminListGateways(w http.ResponseWriter, r *http.Request) {
	list, err := h.Gateways.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

func (h *Handlers) AdminCreateGateway(w http.ResponseWriter, r *http.Request) {
	var body application.CreateGatewayInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	g, err := h.Gateways.Create(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, g)
}

func (h *Handlers) AdminUpdateGateway(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body application.UpdateGatewayInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	body.ID = id
	g, err := h.Gateways.Update(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, g)
}

func (h *Handlers) AdminDeleteGateway(w http.ResponseWriter, r *http.Request) {
	if err := h.Gateways.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) AdminListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := h.Orders.ListAllView(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, orders)
}

// AdminGetOrder devolve um pedido específico com TUDO: custom_data,
// tracking, payment_extra. Hidrata profile (handle, display_name, platform)
// e user (name, email) pra UI mostrar nomes clicáveis em vez de UUIDs.
func (h *Handlers) AdminGetOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ord, err := h.Orders.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	// Estrutura de saída: order com profile{} e user{} embutidos. Nulos
	// se o lookup falhar (perfil deletado, user removido) — front exibe "—".
	out := map[string]any{"order": ord}
	if ord.ProfileID != nil && *ord.ProfileID != "" {
		if p, err := h.Profiles.GetByID(r.Context(), *ord.ProfileID); err == nil && p != nil {
			out["profile"] = map[string]any{
				"id":           p.ID,
				"handle":       p.Handle,
				"display_name": p.DisplayName,
				"platform":     p.Platform,
				"verified":     p.Verified,
			}
		}
	}
	if ord.UserID != "" {
		if u, err := h.Users.GetByID(r.Context(), ord.UserID); err == nil && u != nil {
			out["user"] = map[string]any{
				"id":    u.ID,
				"name":  u.Name,
				"email": u.Email,
			}
		}
	}
	writeData(w, http.StatusOK, out)
}

// AdminPatchOrder permite editar status e nota interna do pedido.
// Status: pending|paid|failed|cancelled. Mudança pra `paid` deveria usar
// /orders/{id}/mark-paid (que dispara os hooks pós-pagamento); aqui
// permitimos pra correção emergencial (não dispara email/webhook).
func (h *Handlers) AdminPatchOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Status *string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	before, _ := h.Orders.GetByID(r.Context(), id)
	if before == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	if body.Status != nil {
		valid := map[string]domain.OrderStatus{
			"pending":   domain.OrderStatusPending,
			"paid":      domain.OrderStatusPaid,
			"failed":    domain.OrderStatusFailed,
			"cancelled": domain.OrderStatusCancelled,
		}
		s, ok := valid[*body.Status]
		if !ok {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		if err := h.Orders.UpdateStatus(r.Context(), id, s, before.ExternalRef); err != nil {
			writeError(w, err)
			return
		}
	}
	after, _ := h.Orders.GetByID(r.Context(), id)
	h.logAudit(r, "update", "order", id, before, after)
	writeData(w, http.StatusOK, after)
}

// AdminCaptureOrderMetrics dispara captura manual de baseline ou delivery
// pra um pedido. Síncrono (10–20s de scrape no máx) — admin clica e
// espera. Body opcional: {"kind":"baseline"|"delivery"} — default baseline.
func (h *Handlers) AdminCaptureOrderMetrics(w http.ResponseWriter, r *http.Request) {
	if h.Metrics == nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	id := chi.URLParam(r, "id")
	kind := "baseline"
	if body, err := io.ReadAll(r.Body); err == nil && len(body) > 0 {
		var b struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(body, &b)
		if b.Kind == "delivery" {
			kind = "delivery"
		}
	}
	var err error
	if kind == "delivery" {
		err = h.Metrics.CaptureDelivery(r.Context(), id)
	} else {
		err = h.Metrics.CaptureBaseline(r.Context(), id)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	// Devolve o order atualizado pra UI re-renderizar imediatamente.
	ord, _ := h.Orders.GetByID(r.Context(), id)
	writeData(w, http.StatusOK, ord)
}

// AdminMetricsSummary alimenta o /dashboard com agregados:
//   - totals por status (pending/paid/failed)
//   - revenue total em USD (settlement_amount somado quando paid)
//   - top 5 categorias por revenue
//   - top 5 países por revenue (extraído de plan_category, ou da
//     tracking — fora de escopo nesse primeiro corte)
//   - serie temporal de 30d (orders/dia)
//
// Tudo computado em memória — escala bem até ~50k orders. Em PRD pode
// virar materialized view.
func (h *Handlers) AdminMetricsSummary(w http.ResponseWriter, r *http.Request) {
	orders, err := h.Orders.ListAllView(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	type byCat struct {
		Category string `json:"category"`
		Orders   int    `json:"orders"`
		Revenue  string `json:"revenue_usd"`
	}
	type daily struct {
		Day     string `json:"day"`
		Orders  int    `json:"orders"`
		Revenue string `json:"revenue_usd"`
	}
	statusCount := map[string]int{}
	catAgg := map[string]struct {
		count   int
		revenue float64
	}{}
	dailyAgg := map[string]struct {
		count   int
		revenue float64
	}{}
	var totalRevenue float64
	var totalPaid int
	for _, o := range orders {
		statusCount[string(o.Status)]++
		amt := float64(o.AmountCents) / 100.0
		day := o.CreatedAt.UTC().Format("2006-01-02")
		dEntry := dailyAgg[day]
		dEntry.count++
		if o.Status == domain.OrderStatusPaid {
			dEntry.revenue += amt
			totalRevenue += amt
			totalPaid++
			cEntry := catAgg[o.PlanCategory]
			cEntry.count++
			cEntry.revenue += amt
			catAgg[o.PlanCategory] = cEntry
		}
		dailyAgg[day] = dEntry
	}

	// top 5 categorias por revenue desc
	cats := make([]byCat, 0, len(catAgg))
	for k, v := range catAgg {
		cats = append(cats, byCat{
			Category: k, Orders: v.count,
			Revenue: strings.TrimRight(strings.TrimRight(formatFloat(v.revenue, 2), "0"), "."),
		})
	}
	// sort manual sem importar "sort" — pequeno e claro
	for i := 1; i < len(cats); i++ {
		j := i
		for j > 0 && parseFloatOr(cats[j].Revenue, 0) > parseFloatOr(cats[j-1].Revenue, 0) {
			cats[j], cats[j-1] = cats[j-1], cats[j]
			j--
		}
	}
	if len(cats) > 5 {
		cats = cats[:5]
	}

	// série diária ordenada (últimos 30 dias)
	days := make([]string, 0, len(dailyAgg))
	for k := range dailyAgg {
		days = append(days, k)
	}
	// sort lexicográfico funciona pra YYYY-MM-DD
	for i := 1; i < len(days); i++ {
		j := i
		for j > 0 && days[j] < days[j-1] {
			days[j], days[j-1] = days[j-1], days[j]
			j--
		}
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	series := make([]daily, 0, 30)
	for _, d := range days {
		if d < cutoff {
			continue
		}
		entry := dailyAgg[d]
		series = append(series, daily{
			Day: d, Orders: entry.count,
			Revenue: formatFloat(entry.revenue, 2),
		})
	}

	writeData(w, http.StatusOK, map[string]any{
		"orders_total":   len(orders),
		"orders_paid":    totalPaid,
		"revenue_usd":    formatFloat(totalRevenue, 2),
		"status_count":   statusCount,
		"top_categories": cats,
		"daily_30d":      series,
	})
}

func formatFloat(f float64, dec int) string {
	if dec == 2 {
		return strconv.FormatFloat(f, 'f', 2, 64)
	}
	return strconv.FormatFloat(f, 'f', dec, 64)
}

func parseFloatOr(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return def
}

func (h *Handlers) AdminListCurrencies(w http.ResponseWriter, r *http.Request) {
	curs, err := h.Currencies.ListAll(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, curs)
}

func (h *Handlers) AdminUpdateCurrency(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var body struct {
		Rate           float64 `json:"rate"`
		DisplayEnabled bool    `json:"display_enabled"`
		SettlementCode string  `json:"settlement_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	// ABAC: mudança grande de taxa (atributo) só por superadmin.
	if current, err := h.Currencies.Get(r.Context(), code); err == nil {
		if application.IsLargeRateChange(current.Rate, body.Rate) {
			if p, ok := principalFromContext(r.Context()); !ok || p.Role != domain.RoleSuperadmin {
				writeError(w, domain.ErrForbidden)
				return
			}
		}
	}
	c, err := h.Currencies.Update(r.Context(), application.UpdateCurrencyInput{
		Code: code, Rate: body.Rate, DisplayEnabled: body.DisplayEnabled, SettlementCode: body.SettlementCode,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, c)
}

// --- Perfis (loja, área logada) --- //

func (h *Handlers) MeListProfiles(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	list, err := h.Profiles.List(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

func (h *Handlers) MeAddProfile(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	var body struct {
		Platform    string `json:"platform"`
		Handle      string `json:"handle"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	p, err := h.Profiles.Add(r.Context(), application.AddProfileInput{
		UserID: userID, Platform: body.Platform, Handle: body.Handle, DisplayName: body.DisplayName,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, p)
}

func (h *Handlers) MeDeleteProfile(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if err := h.Profiles.Delete(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Créditos + ledger --- //

func (h *Handlers) MeCredits(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	acct, err := h.Credits.Balance(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, acct)
}

func (h *Handlers) MeTransactions(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	list, err := h.Credits.History(r.Context(), userID, 200)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

// --- Invoices (recarga de créditos) --- //

func (h *Handlers) MeRecharge(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	var body struct {
		AmountCents     int64  `json:"amount_cents"`
		DisplayCurrency string `json:"display_currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	// Precisamos do e-mail/nome do user para passar pro gateway.
	// Como o handler tem só o userID, pegamos via Orders.ListByUser? Não — o
	// jeito limpo é o service buscar via UserRepository. Pra evitar nova
	// dependência aqui no handler, devolvemos os dados via service que sabe ler.
	inv, err := h.Invoices.Create(r.Context(), application.CreateInvoiceInput{
		UserID:          userID,
		AmountCents:     body.AmountCents,
		DisplayCurrency: body.DisplayCurrency,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, inv)
}

func (h *Handlers) MeListInvoices(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	list, err := h.Invoices.ListForUser(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

// --- Admin: invoices --- //

func (h *Handlers) AdminListInvoices(w http.ResponseWriter, r *http.Request) {
	list, err := h.Invoices.AdminList(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

func (h *Handlers) AdminMarkInvoicePaid(w http.ResponseWriter, r *http.Request) {
	inv, err := h.Invoices.AdminMarkPaid(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, inv)
}

// AdminGetInvoice devolve uma recarga específica com o user hidratado pra
// UI mostrar nome em vez do UUID.
func (h *Handlers) AdminGetInvoice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	inv, err := h.Invoices.AdminGet(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{"invoice": inv}
	if u, err := h.Users.GetByID(r.Context(), inv.UserID); err == nil && u != nil {
		out["user"] = map[string]any{
			"id":    u.ID,
			"name":  u.Name,
			"email": u.Email,
		}
	}
	writeData(w, http.StatusOK, out)
}

// --- Webhooks (público, verificados por assinatura) --- //

func (h *Handlers) WooviWebhook(w http.ResponseWriter, r *http.Request) {
	logger := observability.FromContext(r.Context()).With("provider", "woovi")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		observability.GatewayCallbacksTotal.WithLabelValues("woovi", "bad_request").Inc()
		writeError(w, domain.ErrInvalidInput)
		return
	}
	gw, err := h.Gateways.GetActiveByProvider(r.Context(), "woovi")
	if err != nil || gw == nil {
		observability.GatewayCallbacksTotal.WithLabelValues("woovi", "no_gateway").Inc()
		writeError(w, domain.ErrNotFound)
		return
	}
	if err := payment.VerifyWooviWebhook(body, r.Header.Get("x-webhook-signature"), gw.Config["webhook_secret"]); err != nil {
		observability.GatewayCallbacksTotal.WithLabelValues("woovi", "invalid_signature").Inc()
		logger.Warn("webhook signature invalid", "error", err.Error())
		writeError(w, domain.ErrUnauthorized)
		return
	}
	ev, err := payment.ParseWooviEvent(body)
	if err != nil {
		observability.GatewayCallbacksTotal.WithLabelValues("woovi", "parse_error").Inc()
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if ev.IsPaid() {
		if _, err := h.PaymentReceiver.ConfirmByExternalRef(r.Context(), ev.Charge.Identifier); err != nil {
			observability.GatewayCallbacksTotal.WithLabelValues("woovi", "confirm_failed").Inc()
			logger.Error("ConfirmByExternalRef failed", "error", err.Error())
		} else {
			observability.GatewayCallbacksTotal.WithLabelValues("woovi", "confirmed").Inc()
		}
	} else {
		observability.GatewayCallbacksTotal.WithLabelValues("woovi", "ignored").Inc()
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) HeleketWebhook(w http.ResponseWriter, r *http.Request) {
	logger := observability.FromContext(r.Context()).With("provider", "heleket")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		observability.GatewayCallbacksTotal.WithLabelValues("heleket", "bad_request").Inc()
		writeError(w, domain.ErrInvalidInput)
		return
	}
	gw, err := h.Gateways.GetActiveByProvider(r.Context(), "heleket")
	if err != nil || gw == nil {
		observability.GatewayCallbacksTotal.WithLabelValues("heleket", "no_gateway").Inc()
		writeError(w, domain.ErrNotFound)
		return
	}
	if err := payment.VerifyHeleketWebhook(body, gw.Config["api_key"]); err != nil {
		observability.GatewayCallbacksTotal.WithLabelValues("heleket", "invalid_signature").Inc()
		logger.Warn("webhook signature invalid", "error", err.Error())
		writeError(w, domain.ErrUnauthorized)
		return
	}
	ev, err := payment.ParseHeleketEvent(body)
	if err != nil {
		observability.GatewayCallbacksTotal.WithLabelValues("heleket", "parse_error").Inc()
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if ev.IsPaid() {
		if _, err := h.PaymentReceiver.ConfirmByExternalRef(r.Context(), ev.UUID); err != nil {
			observability.GatewayCallbacksTotal.WithLabelValues("heleket", "confirm_failed").Inc()
			logger.Error("ConfirmByExternalRef failed", "error", err.Error())
		} else {
			observability.GatewayCallbacksTotal.WithLabelValues("heleket", "confirmed").Inc()
		}
	} else {
		observability.GatewayCallbacksTotal.WithLabelValues("heleket", "ignored").Inc()
	}
	w.WriteHeader(http.StatusOK)
}

// --- Admin: users + credits + orders --- //

func (h *Handlers) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Users.ListWithCreditBalance(r.Context(), 200)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, users)
}

func (h *Handlers) AdminGetUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	acct, _ := h.Credits.Balance(r.Context(), id)
	txs, _ := h.Credits.History(r.Context(), id, 100)
	profs, _ := h.Profiles.List(r.Context(), id)
	writeData(w, http.StatusOK, map[string]any{
		"user":         u,
		"credits":      acct,
		"transactions": txs,
		"profiles":     profs,
	})
}

func (h *Handlers) AdminAdjustCredits(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		DeltaCents  int64  `json:"delta_cents"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if body.Description == "" {
		body.Description = "Ajuste manual"
	}
	acct, err := h.Credits.AdminAdjustment(r.Context(), id, body.DeltaCents, body.Description)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, acct)
}

func (h *Handlers) AdminMarkOrderPaid(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.PaymentReceiver.MarkOrderPaid(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResendWebhook recebe eventos da Resend (delivered/bounced/complained).
// Verificação de assinatura via header `svix-signature` (Resend usa Svix).
// Por enquanto sem signature check — Resend Webhook Signing Key fica como
// follow-up; HML é aceitável receber sem auth (endpoint não é mutativo
// pra estado crítico, só atualiza reputation).
//
// Resposta 200 sempre que body parseia. Resend re-tenta em 5xx.
func (h *Handlers) ResendWebhook(w http.ResponseWriter, r *http.Request) {
	logger := observability.FromContext(r.Context()).With("provider", "resend")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	// Svix signature check (Fase 4.4 follow-up). Lemos o secret direto do
	// env pra evitar engordar a struct Handlers — endpoint singleton, custo
	// desprezível por request. Vazio = skip (HML/dev).
	if secret := os.Getenv("RESEND_WEBHOOK_SECRET"); secret != "" {
		svixID := r.Header.Get("svix-id")
		svixTS := r.Header.Get("svix-timestamp")
		svixSig := r.Header.Get("svix-signature")
		if err := email.VerifySvixSignature(body, svixID, svixTS, svixSig, secret); err != nil {
			logger.Warn("svix signature invalid", "error", err.Error())
			writeError(w, domain.ErrUnauthorized)
			return
		}
	}
	if h.EmailRepu == nil {
		// Service não wireado — só registramos e seguimos.
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := h.EmailRepu.RecordResendEvent(r.Context(), body); err != nil {
		logger.Warn("record resend event failed", "error", err.Error())
		writeError(w, domain.ErrInvalidInput)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// PublicValidateCoupon — preview do desconto sem comprometer used_count.
// Front chama isso pra mostrar "$X off com BLACK10" antes do submit.
func (h *Handlers) PublicValidateCoupon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code            string `json:"code"`
		PlanID          string `json:"plan_id"`
		Email           string `json:"email"`
		DisplayCurrency string `json:"display_currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if h.Coupons == nil || h.Plans == nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	plan, err := h.Plans.GetByID(r.Context(), body.PlanID)
	if err != nil {
		writeError(w, err)
		return
	}
	preview, err := h.Coupons.Preview(r.Context(), application.PreviewInput{
		Code:           body.Code,
		AmountUSDCents: plan.PriceCents,
		PlanCategory:   plan.Category,
		UserEmail:      body.Email,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, preview)
}

// Admin CRUD ----

func (h *Handlers) AdminListCoupons(w http.ResponseWriter, r *http.Request) {
	if h.Coupons == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	list, err := h.Coupons.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

func (h *Handlers) AdminCreateCoupon(w http.ResponseWriter, r *http.Request) {
	var c domain.Coupon
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	out, err := h.Coupons.Create(r.Context(), c)
	if err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "create", "coupon", out.Code, nil, out)
	writeData(w, http.StatusCreated, out)
}

func (h *Handlers) AdminUpdateCoupon(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var c domain.Coupon
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	c.Code = code
	before, _ := h.Coupons.Get(r.Context(), code)
	out, err := h.Coupons.Update(r.Context(), c)
	if err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "update", "coupon", code, before, out)
	writeData(w, http.StatusOK, out)
}

func Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// PublicStatus devolve o snapshot do estado dos componentes principais —
// consumido pela página /status do storefront. Cada serviço tem um nome
// curto (mostrado no card) e um status: "operational" | "degraded" | "down".
//
// "degraded" significa que o serviço respondeu mas com indicador anormal
// (ex.: drift > 0 em plan_prices, latência DB > 200ms). "down" é falha
// total. O HTTP status fica sempre 200 — quem consome decide o que mostrar.
func (h *Handlers) PublicStatus(w http.ResponseWriter, r *http.Request) {
	type service struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Detail    string `json:"detail,omitempty"`
		LatencyMs int64  `json:"latency_ms,omitempty"`
	}
	type payload struct {
		Timestamp string    `json:"timestamp"`
		Overall   string    `json:"overall"`
		Services  []service `json:"services"`
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	out := payload{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Services:  make([]service, 0, 4),
	}

	out.Services = append(out.Services, service{Name: "API", Status: "operational"})

	// Database
	dbStart := time.Now()
	dbStatus := "operational"
	dbDetail := ""
	if h.DB != nil {
		if err := h.DB.Pool().Ping(ctx); err != nil {
			dbStatus = "down"
			dbDetail = "ping failed"
		} else if elapsed := time.Since(dbStart); elapsed > 200*time.Millisecond {
			dbStatus = "degraded"
			dbDetail = "slow ping"
		}
	}
	out.Services = append(out.Services, service{
		Name: "Database", Status: dbStatus, Detail: dbDetail,
		LatencyMs: time.Since(dbStart).Milliseconds(),
	})

	// Plan prices invariant — total de drift atual.
	driftStatus := "operational"
	driftDetail := ""
	if h.DB != nil {
		var total int64
		err := h.DB.Pool().QueryRow(ctx, `
			SELECT COUNT(*) FROM plan_prices pp
			JOIN plans p ON p.id=pp.plan_id
			JOIN currencies c ON c.code=pp.currency_code
			WHERE pp.amount::numeric IS DISTINCT FROM
			      ROUND((p.price_cents::numeric / 100.0) * c.rate::numeric, c.decimals)
			  AND pp.amount ~ '^[0-9]+(\.[0-9]+)?$'`).Scan(&total)
		if err != nil {
			driftStatus = "degraded"
			driftDetail = "drift check failed"
		} else if total > 0 {
			driftStatus = "degraded"
			driftDetail = "stale rows in plan_prices"
		}
	}
	out.Services = append(out.Services, service{
		Name: "Plan prices", Status: driftStatus, Detail: driftDetail,
	})

	// Overall = pior status entre os serviços.
	out.Overall = "operational"
	for _, s := range out.Services {
		if s.Status == "down" {
			out.Overall = "down"
			break
		}
		if s.Status == "degraded" {
			out.Overall = "degraded"
		}
	}

	writeData(w, http.StatusOK, out)
}

// ReadyHandler devolve um http.Handler que executa `check` (tipicamente db.Ping).
// 200 quando check==nil ou check() retorna nil; 503 caso contrário.
// O response não vaza o erro pra fora — só status e mensagem genérica. O erro
// completo vai pro log estruturado.
func ReadyHandler(check ReadyChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			if err := check(r); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status": "unavailable",
					"reason": "dependency check failed",
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
}

// MeGetOrder devolve o pedido completo do user logado pra renderizar a
// página de tracking (/account/orders/{id}). Autorização concentrada no
// OrderService — handler só extrai userID + id e delega. ErrNotFound
// quando o pedido não existe OU pertence a outro user, sem distinção.
func (h *Handlers) MeGetOrder(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	o, err := h.OrderSvc.GetByIDForUser(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, o)
}

// PublicCountryPPP devolve o catálogo de multipliers PPP (Fase 6.5). Front
// baixa uma vez por sessão e aplica via priceForCountry() — display_amount
// adaptado ao poder de compra local. USD canonical / settlement intocados.
//
// Lê via h.DB direto pra não exigir nova dep na struct Handlers (main loop
// pluga CountryPPPRepository depois). Países ausentes equivalem a 1.00 — o
// front trata. Pequeno (<50 linhas) → sem paginação.
func (h *Handlers) PublicCountryPPP(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeData(w, http.StatusOK, []domain.CountryPPP{})
		return
	}
	rows, err := h.DB.Pool().Query(r.Context(),
		`SELECT country_code, multiplier FROM country_ppp ORDER BY country_code`,
	)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	out := []domain.CountryPPP{}
	for rows.Next() {
		var p domain.CountryPPP
		if err := rows.Scan(&p.Code, &p.Multiplier); err != nil {
			writeError(w, err)
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, out)
}

// MeGetNotifPrefs — GET /v1/me/notif-prefs
// Devolve as 4 chaves canônicas (order_updates, marketing, reviews,
// cart_recovery) com defaults aplicados quando ausentes. Front usa pra
// renderizar os toggles em /account/notifications.
func (h *Handlers) MeGetNotifPrefs(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.Notifs == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	prefs, err := h.Notifs.GetPrefs(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, prefs)
}

// MeUpdateNotifPrefs — PUT /v1/me/notif-prefs
// Body: { order_updates?: bool, marketing?: bool, reviews?: bool, cart_recovery?: bool }
// Merge no JSONB: chaves ausentes são preservadas; chaves fora da allowlist
// devolvem 400. Idempotente.
func (h *Handlers) MeUpdateNotifPrefs(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.Notifs == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	var body map[string]bool
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if err := h.Notifs.UpdatePrefs(r.Context(), userID, body); err != nil {
		writeError(w, err)
		return
	}
	prefs, err := h.Notifs.GetPrefs(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, prefs)
}

// --- Manage my data (LGPD/GDPR — Fase 5.2) --- //

// MeExportData devolve um JSON com tudo que o sistema sabe do usuário
// (orders, tickets, profiles, reviews, prefs). Force-download via
// Content-Disposition pra UX clara: o usuário clica e o browser salva
// `viralefy-data.json` direto.
func (h *Handlers) MeExportData(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.UserData == nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	data, err := h.UserData.ExportData(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=viralefy-data.json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}

// MeRequestDeletion agenda exclusão da conta. Body opcional: {"reason"}.
// 30 dias de janela pra cancelar antes do hard-delete físico (cron futuro,
// tech debt).
func (h *Handlers) MeRequestDeletion(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.UserData == nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	// Body pode vir vazio — sem reason é OK.
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.UserData.RequestDeletion(r.Context(), userID, body.Reason); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// MeCancelDeletion desfaz um pedido pendente. Idempotente — chamar sem
// request ativa é no-op (204).
func (h *Handlers) MeCancelDeletion(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.UserData == nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if err := h.UserData.CancelDeletion(r.Context(), userID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PublicJWKS expõe a chave pública RSA (Fase 4.1) em
// /.well-known/jwks.json — consumidores externos (verificadores
// stateless) podem validar tokens RS256 sem chamar a API. Lê a chave
// privada que já vive dentro do AuthService pra evitar carregar
// /etc/viralefy/jwt-rs256.pem duas vezes.
func (h *Handlers) PublicJWKS(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || h.Auth.RSAPrivKey == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	jwks, err := jwtkeys.PublicJWKS(h.Auth.RSAPrivKey)
	if err != nil {
		writeError(w, err)
		return
	}
	// Cache curto (5 min) — clientes podem cachear mais tempo via Cache-Control
	// quando rotação for implementada com janela de overlap.
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, jwks)
}

// --- A/B testing harness (Fase 6.6) --- //
//
// Endpoints públicos:
//   POST /v1/ab/assign — devolve variant pra um (visitor_id, experiment_key).
//   POST /v1/ab/track  — registra evento ("exposure"|"conversion"|custom).
//
// Admin (RBAC: admins:manage):
//   GET  /v1/admin/ab/experiments
//   POST /v1/admin/ab/experiments
//   PUT  /v1/admin/ab/experiments/{key}
//
// Visitor ID vem do front (UUID em cookie/localStorage 1y). Sticky
// assignment garante reprodutibilidade entre dispositivos do mesmo visitor.

// PublicABAssign — atribui (ou recupera) a variant do visitor.
// Body: { visitor_id, experiment_key }
// Resp: { variant }
//
// Quando o experimento está inativo, devolve { variant: "control" } como
// fallback seguro — o front renderiza a variant default sem quebrar.
// Quando o experimento não existe, devolve 404.
func (h *Handlers) PublicABAssign(w http.ResponseWriter, r *http.Request) {
	if h.ABTests == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	var body struct {
		VisitorID     string `json:"visitor_id"`
		ExperimentKey string `json:"experiment_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	variant, err := h.ABTests.GetAssignment(r.Context(), body.VisitorID, body.ExperimentKey)
	if err != nil {
		// Inativo → fallback graceful pra "control" sem 4xx.
		if err == domain.ErrExperimentInactive {
			writeData(w, http.StatusOK, map[string]string{"variant": "control"})
			return
		}
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"variant": variant})
}

// PublicABTrack — registra um evento.
// Body: { visitor_id, experiment_key, event_name, payload? }
func (h *Handlers) PublicABTrack(w http.ResponseWriter, r *http.Request) {
	if h.ABTests == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	var body struct {
		VisitorID     string         `json:"visitor_id"`
		ExperimentKey string         `json:"experiment_key"`
		EventName     string         `json:"event_name"`
		Payload       map[string]any `json:"payload,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if err := h.ABTests.TrackEvent(r.Context(), body.VisitorID, body.ExperimentKey, body.EventName, body.Payload); err != nil {
		// Inativo: silenciar (204) — evento não conta mas não é erro pro
		// cliente. Outros erros propagam.
		if err == domain.ErrExperimentInactive {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminListAB — lista todos os experimentos pro backoffice.
func (h *Handlers) AdminListAB(w http.ResponseWriter, r *http.Request) {
	if h.ABTests == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	list, err := h.ABTests.AdminListExperiments(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

// AdminCreateAB — cria experimento.
// Body: { key, description, variants: {variant: weight}, active }
func (h *Handlers) AdminCreateAB(w http.ResponseWriter, r *http.Request) {
	if h.ABTests == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	var e domain.ABExperiment
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	out, err := h.ABTests.AdminCreateExperiment(r.Context(), e)
	if err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "create", "ab_experiment", out.Key, nil, out)
	writeData(w, http.StatusCreated, out)
}

// AdminUpdateAB — atualiza descrição, pesos e flag active. Key imutável.
func (h *Handlers) AdminUpdateAB(w http.ResponseWriter, r *http.Request) {
	if h.ABTests == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	key := chi.URLParam(r, "key")
	var e domain.ABExperiment
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	e.Key = key
	out, err := h.ABTests.AdminUpdateExperiment(r.Context(), e)
	if err != nil {
		writeError(w, err)
		return
	}
	h.logAudit(r, "update", "ab_experiment", key, nil, out)
	writeData(w, http.StatusOK, out)
}

// --- Referrals (Fase 6.4) --- //

// MeGetMyReferral devolve {code, total_referred, total_earned_cents}
// para o painel /account/referral. EnsureCode roda on-demand: usuários
// que nunca acessaram a aba ainda assim ganham código aqui.
func (h *Handlers) MeGetMyReferral(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.Referrals == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	stats, err := h.Referrals.MyStats(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, stats)
}

// PublicReferralInfo é o endpoint anônimo consumido pelo checkout pra
// renderizar o selo "Convidado por X" (primeiro nome apenas). Resposta
// sempre 200 — quando o código não existe, devolve {valid:false} pro
// front degradar silenciosamente sem 404 ruidoso no console.
func (h *Handlers) PublicReferralInfo(w http.ResponseWriter, r *http.Request) {
	if h.Referrals == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	code := chi.URLParam(r, "code")
	info, err := h.Referrals.PublicInfo(r.Context(), code)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, info)
}

// --- Anti-fraude (Fase 4.3) --- //

// AdminListFraudSignals devolve a timeline de sinais gravados pelo
// FraudVelocityCron + checagens inline. Filtros opcionais por actor
// (email/IP substring) e severity (warn|block). Limite default 100.
func (h *Handlers) AdminListFraudSignals(w http.ResponseWriter, r *http.Request) {
	if h.Fraud == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	severity := strings.TrimSpace(r.URL.Query().Get("severity"))
	if severity != "" && severity != "warn" && severity != "block" {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	limit := 0
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	list, err := h.Fraud.ListSignals(r.Context(), actor, severity, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

// PublicTaxRates devolve o catálogo de alíquotas fiscais (Fase 5.3 — VAT
// UE+GB). Front baixa uma vez por sessão e pre-computa a linha de VAT no
// checkout antes do submit. A autoridade do cálculo final é o
// TaxService.ComputeTax server-side; este endpoint serve só pra display.
//
// Tabela pequena (<40 linhas) → sem paginação. Países ausentes equivalem
// a rate 0% e o front trata. Cache-Control fica como o resto dos catálogos
// públicos (CDN/edge caching definido fora daqui).
func (h *Handlers) PublicTaxRates(w http.ResponseWriter, r *http.Request) {
	if h.TaxRates == nil {
		writeData(w, http.StatusOK, []domain.TaxRate{})
		return
	}
	list, err := h.TaxRates.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

// --- Subscriptions (Fase 6.3) ---
//
// Subscriptions são planos mensais recorrentes. O cron de renovação
// (SubscriptionCron) gera uma order pending a cada ciclo via
// CheckoutService.Checkout, e o user paga via payment_url normal.
// 3 falhas seguidas → cancela auto.

// MeListMySubscriptions devolve subs do user autenticado (active +
// cancelled), ordenadas por created_at DESC.
func (h *Handlers) MeListMySubscriptions(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.Subscriptions == nil {
		writeData(w, http.StatusOK, []domain.Subscription{})
		return
	}
	subs, err := h.Subscriptions.ListByUser(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, subs)
}

// MeSubscribe cria sub ativa. Body: {plan_id}. Idempotente (já existir
// active → devolve a mesma). NÃO gera o primeiro pagamento; o user
// continua precisando fazer um checkout manual pro ciclo 0.
func (h *Handlers) MeSubscribe(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.Subscriptions == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	var body struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	sub, err := h.Subscriptions.Subscribe(r.Context(), userID, body.PlanID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, sub)
}

// MeCancelSubscription cancela a sub do user (valida ownership no
// service). DELETE em /v1/me/subscriptions/{id}.
func (h *Handlers) MeCancelSubscription(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	if h.Subscriptions == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if err := h.Subscriptions.Cancel(r.Context(), id, userID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- User behavior tracking (Wave 5) --- //
//
// /v1/track é público (sem auth) — visitor_id é client-supplied via JS no
// browser. Rate-limited via mutationLimiter pra mitigar abuso. Eventos são
// granulares (pageview/click/modal_*/checkout_*/abandon/landing) e populam
// user_events + bumpam user_journeys quando há sessão. Best-effort: erros
// internos viram warn e devolvem 204 (não quebra UX).
//
// /v1/me/journey é a leitura autenticada do agregado + últimos 50 eventos
// do user logado (usado pelo backoffice/account pra ver atribuição).

// PublicTrackEvent — captura evento behavioral.
// Body: { visitor_id, event_type, path?, referrer?, payload?, utm? }.
// event_type whitelist: pageview | click | modal_open | modal_close |
//                        checkout_start | checkout_complete | abandon | landing.
// Quando há JWT user na request, popula user_id automaticamente (cross-
// correlate anônimo→autenticado).
func (h *Handlers) PublicTrackEvent(w http.ResponseWriter, r *http.Request) {
	if h.Events == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	// Cap body em 1MB. Endpoint público sem auth — payload/utm map[string]any
	// poderia receber 100MB JSON e esgotar memória. 1MB cobre largest legítimo
	// (batch de 10 eventos com payload moderado) com folga.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		VisitorID string         `json:"visitor_id"`
		EventType string         `json:"event_type"`
		Path      string         `json:"path"`
		Referrer  string         `json:"referrer"`
		Payload   map[string]any `json:"payload,omitempty"`
		UTM       map[string]any `json:"utm,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if body.VisitorID == "" || body.EventType == "" {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	if !application.IsAllowedEventType(body.EventType) {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	// user_id é opcional — só populado quando o request veio com JWT user.
	uid := userIDFromContext(r.Context())
	in := application.EventInput{
		VisitorID: body.VisitorID,
		UserID:    uid,
		EventType: body.EventType,
		Path:      body.Path,
		Referrer:  body.Referrer,
		Payload:   body.Payload,
		UTM:       body.UTM,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	}
	// RecordEvent é best-effort — não propaga erros (a não ser validação).
	if err := h.Events.RecordEvent(r.Context(), in); err != nil {
		// ErrInvalidInput vem da validação no service (defesa em
		// profundidade). Devolve 400 nesse caso; outros erros já viraram
		// warn no logger e o service retorna nil.
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MeJourney — devolve o agregado do user logado + os últimos 50 eventos.
// Resposta: { journey: UserJourney, events: []UserEvent }.
func (h *Handlers) MeJourney(w http.ResponseWriter, r *http.Request) {
	if h.Events == nil {
		writeError(w, domain.ErrNotFound)
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeError(w, domain.ErrUnauthorized)
		return
	}
	journey, err := h.Events.GetJourney(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	events, err := h.Events.ListByUser(r.Context(), userID, 50)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{
		"journey": journey,
		"events":  events,
	})
}

