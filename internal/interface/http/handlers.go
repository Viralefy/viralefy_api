package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type Handlers struct {
	Plans      *application.PlanService
	Checkout   *application.CheckoutService
	Gateways   *application.GatewayService
	Auth       *application.AuthService
	UserAuth   *application.UserAuthService
	Currencies *application.CurrencyService
	Categories domain.CategoryRepository
	Orders     domain.OrderRepository
	Tickets    *application.TicketService
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

func (h *Handlers) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlanID          string `json:"plan_id"`
		Email           string `json:"email"`
		Name            string `json:"name"`
		Instagram       string `json:"instagram"`
		DisplayCurrency string `json:"display_currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	res, err := h.Checkout.Checkout(r.Context(), application.CheckoutInput{
		PlanID: body.PlanID, Email: body.Email, Name: body.Name,
		Instagram: body.Instagram, DisplayCurrency: body.DisplayCurrency,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

// --- Auth de usuário ---

func (h *Handlers) UserRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email     string `json:"email"`
		Name      string `json:"name"`
		Instagram string `json:"instagram"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	res, err := h.UserAuth.Register(r.Context(), application.RegisterInput{
		Email: body.Email, Name: body.Name, Instagram: body.Instagram, Password: body.Password,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

func (h *Handlers) UserLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
	p, err := h.Plans.Update(r.Context(), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, p)
}

func (h *Handlers) AdminDeletePlan(w http.ResponseWriter, r *http.Request) {
	if err := h.Plans.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func Ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
