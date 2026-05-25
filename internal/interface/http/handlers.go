package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/domain"
)

type Handlers struct {
	Plans    *application.PlanService
	Checkout *application.CheckoutService
	Gateways *application.GatewayService
	Auth     *application.AuthService
	Orders   domain.OrderRepository
}

func (h *Handlers) ListPublicPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := h.Plans.ListPublic(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plans)
}

func (h *Handlers) Checkout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlanID    string `json:"plan_id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		Instagram string `json:"instagram"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, domain.ErrInvalidInput)
		return
	}
	res, err := h.Checkout.Checkout(r.Context(), application.CheckoutInput{
		PlanID: body.PlanID, Email: body.Email, Name: body.Name,
		Instagram: body.Instagram, Password: body.Password,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, res)
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
	orders, err := h.Orders.ListAll(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, orders)
}

func Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func Ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
