package application

import "context"

// PaymentCustomer são os dados mínimos do cliente que o provider precisa
// (Woovi e Heleket pedem isso para emitir cobrança / antifraude).
type PaymentCustomer struct {
	Name  string
	Email string
}

// PaymentChargeInput é o que vai para o adapter do provider.
type PaymentChargeInput struct {
	OrderID     string
	Description string
	Amount      string // string formatada (ex.: "9.90", "0.00018"), decimais conforme a moeda
	Currency    string // BRL, USDT, BTC, USD, EUR
	Customer    PaymentCustomer
	Config      map[string]string // config do gateway (app_id, api_key, base_url, callback_url, ...)
}

// PaymentCharge é a resposta normalizada do provider.
type PaymentCharge struct {
	ExternalRef string            // id da cobrança no provider
	PaymentURL  string            // link para a tela/QR do pagamento
	Extra       map[string]string // br_code, qr_code_image, wallet, network, expires_at, ...
}

// PaymentProvider é a porta de saída para a integração com gateways de
// pagamento. Cada implementação concreta vive em infrastructure/external/payment.
type PaymentProvider interface {
	Provider() string // identificador (ex.: "woovi", "heleket", "manual_pix")
	CreateCharge(ctx context.Context, in PaymentChargeInput) (PaymentCharge, error)
}

// PaymentRegistry agrega os providers disponíveis, indexados pelo identificador.
type PaymentRegistry struct {
	providers map[string]PaymentProvider
}

func NewPaymentRegistry(list ...PaymentProvider) *PaymentRegistry {
	r := &PaymentRegistry{providers: map[string]PaymentProvider{}}
	for _, p := range list {
		r.providers[p.Provider()] = p
	}
	return r
}

func (r *PaymentRegistry) Get(provider string) (PaymentProvider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[provider]
	return p, ok
}
