package postgres

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func Seed(ctx context.Context, db *DB) error {
	if err := seedCategories(ctx, db); err != nil {
		return err
	}
	if err := seedCurrencies(ctx, db); err != nil {
		return err
	}
	if err := seedPlans(ctx, db); err != nil {
		return err
	}
	if err := seedGateway(ctx, db); err != nil {
		return err
	}
	if err := seedRoles(ctx, db); err != nil {
		return err
	}
	return seedAdmin(ctx, db)
}

func seedRoles(ctx context.Context, db *DB) error {
	roles := []struct {
		code, label string
		perms       []string
	}{
		{"superadmin", "Super Admin", []string{
			"plans:read", "plans:write", "gateways:read", "gateways:write",
			"currencies:read", "currencies:write", "orders:read", "admins:manage",
		}},
		{"manager", "Gerente", []string{
			"plans:read", "plans:write", "gateways:read", "gateways:write",
			"currencies:read", "currencies:write", "orders:read",
		}},
		{"support", "Suporte", []string{
			"plans:read", "gateways:read", "currencies:read", "orders:read",
		}},
		{"viewer", "Leitura", []string{
			"plans:read", "gateways:read", "currencies:read", "orders:read",
		}},
	}
	for _, r := range roles {
		if _, err := db.pool.Exec(ctx, `
			INSERT INTO roles (code, label) VALUES ($1,$2)
			ON CONFLICT (code) DO UPDATE SET label = EXCLUDED.label`, r.code, r.label); err != nil {
			return err
		}
		for _, p := range r.perms {
			if _, err := db.pool.Exec(ctx, `
				INSERT INTO role_permissions (role_code, permission) VALUES ($1,$2)
				ON CONFLICT DO NOTHING`, r.code, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedCategories(ctx context.Context, db *DB) error {
	cats := []struct {
		code, label string
		order       int
	}{
		{"seguidores", "Seguidores", 1},
		{"engajamento", "Engajamento", 2},
		{"visualizacoes", "Visualizações", 3},
		{"servicos", "Serviços", 4},
	}
	for _, c := range cats {
		_, err := db.pool.Exec(ctx, `
			INSERT INTO categories (code, label, sort_order, active)
			VALUES ($1,$2,$3,true) ON CONFLICT (code) DO UPDATE SET label=EXCLUDED.label, sort_order=EXCLUDED.sort_order`,
			c.code, c.label, c.order)
		if err != nil {
			return err
		}
	}
	return nil
}

func seedCurrencies(ctx context.Context, db *DB) error {
	// rate = unidades da moeda por 1 BRL. base = BRL (rate 1).
	curs := []struct {
		code, name, symbol, kind, settlement string
		rate                                 float64
		decimals, order                      int
		display                              bool
	}{
		{"BRL", "Real", "R$", "fiat", "BRL", 1.0, 2, 1, true},
		{"USD", "Dólar", "$", "fiat", "USDT", 0.185, 2, 2, true},
		{"EUR", "Euro", "€", "fiat", "EUR", 0.17, 2, 3, true},
		{"BTC", "Bitcoin", "₿", "crypto", "BTC", 0.0000019, 8, 4, true},
		{"USDT", "Tether", "₮", "crypto", "USDT", 0.185, 2, 5, false},
	}
	for _, c := range curs {
		_, err := db.pool.Exec(ctx, `
			INSERT INTO currencies (code, name, symbol, rate, decimals, kind, display_enabled, settlement_code, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (code) DO NOTHING`,
			c.code, c.name, c.symbol, c.rate, c.decimals, c.kind, c.display, c.settlement, c.order)
		if err != nil {
			return err
		}
	}
	return nil
}

func seedPlans(ctx context.Context, db *DB) error {
	var n int
	_ = db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM plans`).Scan(&n)
	if n > 0 {
		return nil
	}
	// Planos-base de seguidores com os valores BRL definidos pelo cliente,
	// mais serviços complementares. brl é o preço base; os preços nas demais
	// moedas são gerados como ponto de partida editável (preço manual por moeda).
	plans := []struct {
		name, desc, category string
		qty                  int
		brl                  float64
		order                int
	}{
		{"100 seguidores", "Ideal para testar", "seguidores", 100, 9.90, 1},
		{"250 seguidores", "Primeiro impulso", "seguidores", 250, 18.90, 2},
		{"500 seguidores", "Crescimento inicial", "seguidores", 500, 35.90, 3},
		{"1.000 seguidores", "Mais alcance", "seguidores", 1000, 69.90, 4},
		{"10.000 seguidores", "Escala", "seguidores", 10000, 399.90, 5},
		{"100.000 seguidores", "Autoridade", "seguidores", 100000, 3199.90, 6},
		{"1.000.000 seguidores", "Máximo alcance", "seguidores", 1000000, 11999.99, 7},
		{"Curtidas 1k", "1.000 curtidas distribuídas", "engajamento", 1000, 14.90, 1},
		{"Curtidas 5k", "5.000 curtidas + comentários", "engajamento", 5000, 59.90, 2},
		{"Views 10k", "10.000 visualizações em Reels", "visualizacoes", 10000, 12.90, 1},
		{"Views 50k", "50.000 visualizações em Reels", "visualizacoes", 50000, 49.90, 2},
		{"Gestão Mensal", "Gestão de perfil + estratégia", "servicos", 1, 299.90, 1},
	}
	for _, p := range plans {
		id := uuid.New().String()
		cents := int(p.brl*100 + 0.5)
		_, err := db.pool.Exec(ctx, `
			INSERT INTO plans (id, name, description, category, followers_qty, price_cents, currency, active, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6,'BRL',true,$7)`,
			id, p.name, p.desc, p.category, p.qty, cents, p.order)
		if err != nil {
			return err
		}
		if err := seedPlanPrices(ctx, db, id, p.brl); err != nil {
			return err
		}
	}
	return nil
}

// seedPlanPrices gera um preço inicial por moeda a partir do BRL. Valores
// editáveis no backoffice (a fonte da verdade é o preço manual por moeda).
func seedPlanPrices(ctx context.Context, db *DB, planID string, brl float64) error {
	rows, err := db.pool.Query(ctx, `SELECT code, rate, decimals FROM currencies`)
	if err != nil {
		return err
	}
	type cur struct {
		code     string
		rate     float64
		decimals int
	}
	var curs []cur
	for rows.Next() {
		var c cur
		if err := rows.Scan(&c.code, &c.rate, &c.decimals); err != nil {
			rows.Close()
			return err
		}
		curs = append(curs, c)
	}
	rows.Close()
	for _, c := range curs {
		var amount string
		if c.code == "BRL" {
			amount = strconv.FormatFloat(brl, 'f', 2, 64)
		} else {
			amount = strconv.FormatFloat(brl*c.rate, 'f', c.decimals, 64)
		}
		if _, err := db.pool.Exec(ctx, `
			INSERT INTO plan_prices (plan_id, currency_code, amount) VALUES ($1,$2,$3)
			ON CONFLICT (plan_id, currency_code) DO NOTHING`, planID, c.code, amount); err != nil {
			return err
		}
	}
	return nil
}

func seedGateway(ctx context.Context, db *DB) error {
	// Idempotente por provider — adiciona o que faltar sem mexer no existente.
	gws := []struct{ name, provider, config string; active bool }{
		{"PIX Manual", "manual_pix", `{"pix_key":"contato@viralefy.com"}`, true},
		{"Woovi (PIX)", "woovi", `{"app_id":"","base_url":"https://api.woovi.com.br"}`, false},
		{"Heleket (cripto)", "heleket", `{"merchant_id":"","api_key":"","base_url":"https://api.heleket.com","url_callback":""}`, false},
	}
	for _, g := range gws {
		var exists int
		_ = db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payment_gateways WHERE provider=$1`, g.provider).Scan(&exists)
		if exists > 0 {
			continue
		}
		if _, err := db.pool.Exec(ctx, `
			INSERT INTO payment_gateways (id, name, provider, active, config)
			VALUES ($1,$2,$3,$4,$5::jsonb)`,
			uuid.New().String(), g.name, g.provider, g.active, g.config); err != nil {
			return err
		}
	}
	return nil
}

func seedAdmin(ctx context.Context, db *DB) error {
	var n int
	_ = db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	if n > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("SimTest!Admin2026"), 12)
	if err != nil {
		return err
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO admins (id, email, password_hash, name)
		VALUES ($1,'admin@viralefy.local',$2,'Administrador')`, uuid.New().String(), string(hash))
	return err
}
