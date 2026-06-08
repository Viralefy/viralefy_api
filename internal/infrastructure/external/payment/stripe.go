package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/viralefy/viralefy_api/internal/application"
)

// Stripe — Checkout Session API. Provider gera uma sessão hospedada por
// pedido e retorna a URL pra redirecionar o cliente. Sem libs externas: o
// stripe-go bate em 100+ deps; aqui chamamos a REST API direto.
//
// Config esperado:
//   secret_key   — sk_live_... (obrigatório)
//   success_url  — opcional, default {siteURL}/account/orders/{order_id}
//   cancel_url   — opcional, default {siteURL}/checkout/cancelled
//   payment_method_types — opcional, default "card" (CSV: card,link,...)
//
// Webhook signature check + auto mark-as-paid ficam separados (próximo
// commit). Por ora o cliente paga via Stripe, retorna ao success_url e o
// PaymentReceiver é acionado manualmente — exatamente como manual_pix.
type Stripe struct {
	client  *http.Client
	siteURL string
}

func NewStripe(siteURL string) *Stripe {
	return &Stripe{
		client:  &http.Client{Timeout: 20 * time.Second},
		siteURL: strings.TrimRight(siteURL, "/"),
	}
}

func (*Stripe) Provider() string { return "stripe" }

func (s *Stripe) CreateCharge(ctx context.Context, in application.PaymentChargeInput) (application.PaymentCharge, error) {
	secret := strings.TrimSpace(in.Config["secret_key"])
	if secret == "" {
		return application.PaymentCharge{}, fmt.Errorf("stripe: missing secret_key in config")
	}

	successURL := strings.TrimSpace(in.Config["success_url"])
	if successURL == "" {
		successURL = s.siteURL + "/account/orders/" + in.OrderID
	}
	cancelURL := strings.TrimSpace(in.Config["cancel_url"])
	if cancelURL == "" {
		cancelURL = s.siteURL + "/checkout/cancelled?order_id=" + in.OrderID
	}
	methods := strings.TrimSpace(in.Config["payment_method_types"])
	if methods == "" {
		methods = "card"
	}

	cents, err := amountToMinorUnits(in.Amount)
	if err != nil {
		return application.PaymentCharge{}, fmt.Errorf("stripe: amount: %w", err)
	}

	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	form.Set("client_reference_id", in.OrderID)
	if in.Customer.Email != "" {
		form.Set("customer_email", in.Customer.Email)
	}
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", strings.ToLower(in.Currency))
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(cents, 10))
	form.Set("line_items[0][price_data][product_data][name]", in.Description)
	form.Set("metadata[order_id]", in.OrderID)
	// payment_method_types[]=card&payment_method_types[]=link
	for i, m := range strings.Split(methods, ",") {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		form.Set(fmt.Sprintf("payment_method_types[%d]", i), m)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.stripe.com/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return application.PaymentCharge{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(secret, "")

	resp, err := s.client.Do(req)
	if err != nil {
		return application.PaymentCharge{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return application.PaymentCharge{}, fmt.Errorf("stripe: HTTP %d: %s", resp.StatusCode, truncateStripe(string(body), 300))
	}
	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		return application.PaymentCharge{}, err
	}
	return application.PaymentCharge{
		ExternalRef: session.ID,
		PaymentURL:  session.URL,
		Extra: map[string]string{
			"method_kind": "card",
			"provider":    "stripe",
		},
	}, nil
}

// amountToMinorUnits converte "9.90" -> 990 (assumindo 2 decimals). Stripe
// usa minor units pra TODAS as moedas relevantes (BRL, USD, EUR, GBP).
// JPY/KRW seriam zero-decimal — quando precisar, mapeamos.
func amountToMinorUnits(amount string) (int64, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return 0, fmt.Errorf("empty amount")
	}
	negative := false
	if strings.HasPrefix(amount, "-") {
		negative = true
		amount = amount[1:]
	}
	parts := strings.SplitN(amount, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	var dec int64
	if len(parts) == 2 {
		d := parts[1]
		if len(d) > 2 {
			d = d[:2]
		}
		for len(d) < 2 {
			d += "0"
		}
		dec, err = strconv.ParseInt(d, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	out := whole*100 + dec
	if negative {
		out = -out
	}
	return out, nil
}

func truncateStripe(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
