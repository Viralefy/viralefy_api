package domain

import "context"

// Currency descreve uma moeda suportada.
// Rate = unidades desta moeda por 1 BRL (moeda base). SettlementCode indica
// em qual moeda a cobrança é efetivamente liquidada (ex.: USD é exibida, mas
// liquidada em USDT).
type Currency struct {
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	Symbol         string  `json:"symbol"`
	Rate           float64 `json:"rate"`
	Decimals       int     `json:"decimals"`
	Kind           string  `json:"kind"` // fiat | crypto
	DisplayEnabled bool    `json:"display_enabled"`
	SettlementCode string  `json:"settlement_code"`
	SortOrder      int     `json:"sort_order"`
}

type CurrencyRepository interface {
	ListAll(ctx context.Context) ([]Currency, error)
	ListDisplayable(ctx context.Context) ([]Currency, error)
	GetByCode(ctx context.Context, code string) (*Currency, error)
	UpdateRate(ctx context.Context, code string, rate float64, displayEnabled bool, settlementCode string) error
}
