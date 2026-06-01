package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/domain"
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
	DB      *postgres.DB
	Metrics *application.MetricCaptureService
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
		Handle              string `json:"handle"`                 // @handle alvo
		Platform            string `json:"platform"`               // instagram | tiktok
		BanDate             string `json:"ban_date"`               // ISO 8601 ou texto livre
		EstimatedReason     string `json:"estimated_reason"`       // suspeita do usuário
		LastPublicationURL  string `json:"last_publication_url"`   // último post visível
		Description         string `json:"description"`            // contexto extra
		ContactEmail        string         `json:"contact_email"`
		ContactName         string         `json:"contact_name"`
		DisplayCurrency     string         `json:"display_currency"`
		PaymentMethod       string         `json:"payment_method,omitempty"`
		TurnstileToken      string         `json:"turnstile_token"`
		Tracking            map[string]any `json:"tracking,omitempty"`
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
		"orders_total":  len(orders),
		"orders_paid":   totalPaid,
		"revenue_usd":   formatFloat(totalRevenue, 2),
		"status_count":  statusCount,
		"top_categories": cats,
		"daily_30d":     series,
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

func Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
