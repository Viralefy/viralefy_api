package application

import (
	"context"
	"strings"

	"github.com/viralefy/viralefy_api/internal/domain"
)

// PaymentMethodOption descreve um método de pagamento DISPONÍVEL pra um
// pedido específico. O cliente vê uma lista desses cards no checkout e
// escolhe um. Cada opção carrega:
//   - GatewayID  — id a ser passado no POST /v1/checkout
//   - Kind       — card | pix | crypto_manual | crypto_auto (UI escolhe ícone)
//   - ChargedAmount/Currency — o que ele EFETIVAMENTE paga (ex.: R$50,00 BRL)
//   - SettlementAmount/Currency — o que cai na plataforma (ex.: 10.00 USDT)
//   - ConversionNote — string de transparência ("você paga R$50, a plataforma
//                      recebe 10 USDT após conversão"). Só populada quando
//                      Charged ≠ Settlement.
//   - NetworkLabel/NetworkWarning — pra crypto: "USDT (TRC20)" + aviso.
type PaymentMethodOption struct {
	GatewayID          string `json:"gateway_id"`
	Provider           string `json:"provider"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	ChargedCurrency    string `json:"charged_currency"`
	ChargedAmount      string `json:"charged_amount"`
	ChargedSymbol      string `json:"charged_symbol"`
	SettlementCurrency string `json:"settlement_currency"`
	SettlementAmount   string `json:"settlement_amount"`
	SettlementSymbol   string `json:"settlement_symbol"`
	DisplayCurrency    string `json:"display_currency"`
	DisplayAmount      string `json:"display_amount"`
	ConversionNote     string `json:"conversion_note,omitempty"`
	NetworkLabel       string `json:"network_label,omitempty"`
	NetworkWarning     string `json:"network_warning,omitempty"`
}

// ListPaymentMethods retorna os métodos de pagamento aceitos pra um plano,
// já com o preview de quanto o cliente vai pagar EM CADA gateway. Não cria
// pedido — é só o catálogo pra UI montar a lista de cards.
//
// Algoritmo:
//   1. resolve quote padrão (display + settlement por currency.settlement_code)
//   2. lista TODOS os gateways ativos
//   3. pra cada gateway, escolhe a currency natural dele:
//      - se aceita a settlement currency → usa
//      - senão usa a primeira da lista (ex.: PIX só aceita BRL)
//   4. computa charged_amount nessa currency usando amountFor
//   5. monta conversion_note quando charged ≠ settlement
//
// Filtros (futuros): por país (ex.: PIX só BR). Não bloqueamos hoje porque
// não conhecemos a heurística país→método sem mapping explícito; deixamos
// a UI esconder o que não fizer sentido (ex.: PIX se country != "br").
func (s *CheckoutService) ListPaymentMethods(
	ctx context.Context, planID, displayCurrency, country string,
) ([]PaymentMethodOption, error) {
	plan, err := s.plans.GetByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if !plan.Active {
		return nil, domain.ErrInvalidInput
	}
	quote, err := s.currencies.QuoteForPlan(ctx, plan.Prices, plan.PriceCents, displayCurrency)
	if err != nil {
		return nil, err
	}
	all, err := s.gateways.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PaymentMethodOption, 0, len(all))
	for _, g := range all {
		if !g.Active {
			continue
		}
		if !gatewayEligible(g, quote.DisplayCurrency, quote.SettlementCurrency, country) {
			continue
		}
		opt, ok := s.buildMethodOption(ctx, g, plan, quote)
		if !ok {
			continue
		}
		out = append(out, opt)
	}
	return out, nil
}

// gatewayEligible decide se um gateway deve aparecer pro cliente. Regra:
//   - Gateway aceita display_currency OU settlement_currency → mostra
//   - PIX (BRL) só aparece se o cliente está em BRL OU country=br (mesmo
//     que o display seja outro — ex.: brasileiro vendo preço em USD)
//   - "Universal" providers (Stripe/heleket) ficam visíveis sempre que a
//     settlement currency bater
//
// Sem isso, o catálogo retornava TUDO e o cliente alemão via "Pay R$X
// via PIX" — útil pra ninguém, confuso pra todos.
func gatewayEligible(g domain.PaymentGateway, displayCurrency, settlementCurrency, country string) bool {
	display := strings.ToUpper(strings.TrimSpace(displayCurrency))
	settle := strings.ToUpper(strings.TrimSpace(settlementCurrency))
	country = strings.ToLower(strings.TrimSpace(country))
	for _, raw := range g.AcceptedCurrencies {
		c := strings.ToUpper(strings.TrimSpace(raw))
		if c == display || c == settle {
			return true
		}
		// PIX/BRL: brasileiro navegando em USD/EUR ainda deve ver PIX.
		// Outros tipos de gateway BRL-only seguem a mesma regra.
		if c == "BRL" && country == "br" {
			return true
		}
	}
	return false
}

// buildMethodOption monta uma PaymentMethodOption pra um gateway específico.
// Retorna ok=false quando o gateway não tem moeda válida pra cobrar (gateway
// mal cadastrado).
func (s *CheckoutService) buildMethodOption(
	ctx context.Context, g domain.PaymentGateway, plan *domain.Plan, quote Quote,
) (PaymentMethodOption, bool) {
	if len(g.AcceptedCurrencies) == 0 {
		return PaymentMethodOption{}, false
	}
	chargedCurrency := pickChargedCurrency(g.AcceptedCurrencies, quote.SettlementCurrency)
	cur, err := s.currencies.repo.GetByCode(ctx, chargedCurrency)
	if err != nil || cur == nil {
		return PaymentMethodOption{}, false
	}
	chargedAmount := amountFor(plan.Prices, plan.PriceCents, *cur)
	settleAmount := chargedAmount
	settleCurrency := chargedCurrency
	settleSymbol := cur.Symbol
	if !strings.EqualFold(chargedCurrency, quote.SettlementCurrency) {
		settleAmount = quote.SettlementAmount
		settleCurrency = quote.SettlementCurrency
		settleSymbol = quote.SettlementSymbol
	}
	opt := PaymentMethodOption{
		GatewayID:          g.ID,
		Provider:           g.Provider,
		Name:               g.Name,
		Kind:               kindOf(g.Provider),
		ChargedCurrency:    chargedCurrency,
		ChargedAmount:      chargedAmount,
		ChargedSymbol:      cur.Symbol,
		SettlementCurrency: settleCurrency,
		SettlementAmount:   settleAmount,
		SettlementSymbol:   settleSymbol,
		DisplayCurrency:    quote.DisplayCurrency,
		DisplayAmount:      quote.DisplayAmount,
	}
	if !strings.EqualFold(chargedCurrency, settleCurrency) {
		opt.ConversionNote = "You pay " + chargedAmount + " " + chargedCurrency +
			"; the platform receives " + settleAmount + " " + settleCurrency +
			" after conversion."
	}
	if g.Provider == "manual_crypto" || g.Provider == "manual_usdt" {
		if net := strings.TrimSpace(g.Config["network"]); net != "" {
			opt.NetworkLabel = strings.TrimSpace(g.Config["network_label"])
			if opt.NetworkLabel == "" {
				opt.NetworkLabel = chargedCurrency + " (" + net + ")"
			}
			opt.NetworkWarning = strings.TrimSpace(g.Config["network_warning"])
			if opt.NetworkWarning == "" {
				opt.NetworkWarning = "Send ONLY on the " + net +
					" network. Deposits on any other network will be lost forever."
			}
		}
	}
	return opt, true
}

// pickChargedCurrency escolhe a moeda em que o gateway efetivamente cobra.
// Heurística:
//   - se aceita a settlement (USDT na maioria dos casos) → cobra em settlement
//   - senão pega a primeira da lista (ex.: Woovi/manual_pix só BRL)
// Evita decisão errada como mostrar "Pague R$50 em USDT" pra um gateway PIX.
func pickChargedCurrency(accepted []string, settlement string) string {
	settlement = strings.ToUpper(settlement)
	for _, c := range accepted {
		if strings.ToUpper(c) == settlement {
			return settlement
		}
	}
	return strings.ToUpper(strings.TrimSpace(accepted[0]))
}

// kindOf mapeia provider → kind genérico (UI usa pra ícone/etiqueta).
func kindOf(provider string) string {
	switch provider {
	case "woovi", "manual_pix":
		return "pix"
	case "stripe":
		return "card"
	case "manual_crypto", "manual_usdt":
		return "crypto_manual"
	case "heleket":
		return "crypto_auto"
	}
	return "other"
}

