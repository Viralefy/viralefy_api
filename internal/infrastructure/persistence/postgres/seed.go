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
			"currencies:read", "currencies:write", "orders:read",
			"tickets:read", "tickets:write", "admins:manage",
		}},
		{"manager", "Gerente", []string{
			"plans:read", "plans:write", "gateways:read", "gateways:write",
			"currencies:read", "currencies:write", "orders:read",
			"tickets:read", "tickets:write",
		}},
		{"support", "Suporte", []string{
			"plans:read", "gateways:read", "currencies:read", "orders:read",
			"tickets:read", "tickets:write",
		}},
		{"viewer", "Leitura", []string{
			"plans:read", "gateways:read", "currencies:read", "orders:read",
			"tickets:read",
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
		{"curtidas", "Curtidas", 5},
		{"comentarios", "Comentários", 6},
		{"compartilhamentos", "Compartilhamentos", 7},
		{"salvamentos", "Salvamentos", 8},
		{"reels", "Reels", 9},
		{"stories", "Stories", 10},
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
	// Idempotente por (name, category). Permite acrescentar planos novos em
	// versões futuras sem destruir os existentes (preços manuais sobrevivem).
	type planSeed struct {
		name, desc, category, platform, target string
		qty                                    int
		brl                                    float64
		order                                  int
	}
	plans := []planSeed{
		// ===== INSTAGRAM ===== //
		// ---- Seguidores (perfil) ----
		{"100 seguidores Instagram", "Ideal para testar", "seguidores", "instagram", "profile", 100, 9.90, 1},
		{"250 seguidores Instagram", "Primeiro impulso", "seguidores", "instagram", "profile", 250, 18.90, 2},
		{"500 seguidores Instagram", "Crescimento inicial", "seguidores", "instagram", "profile", 500, 35.90, 3},
		{"1.000 seguidores Instagram", "Mais alcance", "seguidores", "instagram", "profile", 1000, 69.90, 4},
		{"2.500 seguidores Instagram", "Tração", "seguidores", "instagram", "profile", 2500, 149.90, 5},
		{"5.000 seguidores Instagram", "Comunidade", "seguidores", "instagram", "profile", 5000, 249.90, 6},
		{"10.000 seguidores Instagram", "Escala", "seguidores", "instagram", "profile", 10000, 399.90, 7},
		{"25.000 seguidores Instagram", "Microinfluência", "seguidores", "instagram", "profile", 25000, 899.90, 8},
		{"50.000 seguidores Instagram", "Influência consolidada", "seguidores", "instagram", "profile", 50000, 1699.90, 9},
		{"100.000 seguidores Instagram", "Autoridade", "seguidores", "instagram", "profile", 100000, 3199.90, 10},
		{"250.000 seguidores Instagram", "Marca pessoal", "seguidores", "instagram", "profile", 250000, 6499.90, 11},
		{"500.000 seguidores Instagram", "Top tier", "seguidores", "instagram", "profile", 500000, 8999.90, 12},
		{"1.000.000 seguidores Instagram", "Máximo alcance", "seguidores", "instagram", "profile", 1000000, 11999.99, 13},

		// ---- Curtidas IG (publicação) ----
		{"100 curtidas Instagram", "Boost inicial", "curtidas", "instagram", "publication", 100, 4.90, 1},
		{"500 curtidas Instagram", "Engajamento médio", "curtidas", "instagram", "publication", 500, 14.90, 2},
		{"1.000 curtidas Instagram", "Alta visibilidade", "curtidas", "instagram", "publication", 1000, 24.90, 3},
		{"5.000 curtidas Instagram", "Viralizando", "curtidas", "instagram", "publication", 5000, 89.90, 4},
		{"10.000 curtidas Instagram", "Em alta", "curtidas", "instagram", "publication", 10000, 149.90, 5},
		{"50.000 curtidas Instagram", "Top do feed", "curtidas", "instagram", "publication", 50000, 599.90, 6},
		{"100.000 curtidas Instagram", "Explosão", "curtidas", "instagram", "publication", 100000, 999.90, 7},

		// ---- Comentários IG (publicação) ----
		{"50 comentários Instagram", "Conversa inicial", "comentarios", "instagram", "publication", 50, 19.90, 1},
		{"100 comentários Instagram", "Engajamento real", "comentarios", "instagram", "publication", 100, 34.90, 2},
		{"250 comentários Instagram", "Discussão ativa", "comentarios", "instagram", "publication", 250, 79.90, 3},
		{"500 comentários Instagram", "Comunidade", "comentarios", "instagram", "publication", 500, 149.90, 4},
		{"1.000 comentários Instagram", "Viral", "comentarios", "instagram", "publication", 1000, 269.90, 5},

		// ---- Compartilhamentos IG (publicação) ----
		{"100 compartilhamentos Instagram", "Espalha a notícia", "compartilhamentos", "instagram", "publication", 100, 12.90, 1},
		{"500 compartilhamentos Instagram", "Reach extra", "compartilhamentos", "instagram", "publication", 500, 49.90, 2},
		{"1.000 compartilhamentos Instagram", "Conteúdo em alta", "compartilhamentos", "instagram", "publication", 1000, 89.90, 3},
		{"5.000 compartilhamentos Instagram", "Viralidade real", "compartilhamentos", "instagram", "publication", 5000, 399.90, 4},

		// ---- Salvamentos IG (publicação) ----
		{"100 salvamentos Instagram", "Conteúdo de valor", "salvamentos", "instagram", "publication", 100, 9.90, 1},
		{"500 salvamentos Instagram", "Referência", "salvamentos", "instagram", "publication", 500, 39.90, 2},
		{"1.000 salvamentos Instagram", "Top of mind", "salvamentos", "instagram", "publication", 1000, 69.90, 3},
		{"5.000 salvamentos Instagram", "Conteúdo evergreen", "salvamentos", "instagram", "publication", 5000, 299.90, 4},

		// ---- Reels IG (publicação) ----
		{"1.000 views em Reels Instagram", "Pickup inicial", "reels", "instagram", "publication", 1000, 4.90, 1},
		{"10.000 views em Reels Instagram", "Em alta", "reels", "instagram", "publication", 10000, 12.90, 2},
		{"50.000 views em Reels Instagram", "Trending", "reels", "instagram", "publication", 50000, 49.90, 3},
		{"100.000 views em Reels Instagram", "Boom", "reels", "instagram", "publication", 100000, 89.90, 4},
		{"500.000 views em Reels Instagram", "Viral", "reels", "instagram", "publication", 500000, 379.90, 5},
		{"1.000.000 views em Reels Instagram", "Hit nacional", "reels", "instagram", "publication", 1000000, 699.90, 6},

		// ---- Stories IG (perfil — Stories somam por conta) ----
		{"500 views em Stories Instagram", "Boost de Stories", "stories", "instagram", "profile", 500, 6.90, 1},
		{"2.000 views em Stories Instagram", "Alta presença", "stories", "instagram", "profile", 2000, 19.90, 2},
		{"10.000 views em Stories Instagram", "Massivo", "stories", "instagram", "profile", 10000, 79.90, 3},

		// ===== TIKTOK ===== //
		// ---- Seguidores TikTok (perfil) ----
		{"500 seguidores TikTok", "Primeiro empurrão", "seguidores", "tiktok", "profile", 500, 29.90, 14},
		{"1.000 seguidores TikTok", "Decola", "seguidores", "tiktok", "profile", 1000, 54.90, 15},
		{"5.000 seguidores TikTok", "Crescimento sólido", "seguidores", "tiktok", "profile", 5000, 199.90, 16},
		{"10.000 seguidores TikTok", "Escala TikTok", "seguidores", "tiktok", "profile", 10000, 349.90, 17},
		{"50.000 seguidores TikTok", "Creator", "seguidores", "tiktok", "profile", 50000, 1499.90, 18},
		{"100.000 seguidores TikTok", "Influência TikTok", "seguidores", "tiktok", "profile", 100000, 2799.90, 19},
		{"500.000 seguidores TikTok", "Top creator", "seguidores", "tiktok", "profile", 500000, 7999.90, 20},
		{"1.000.000 seguidores TikTok", "Viral total", "seguidores", "tiktok", "profile", 1000000, 10999.90, 21},

		// ---- Curtidas TikTok (publicação = vídeo) ----
		{"500 curtidas TikTok", "Boost de vídeo", "curtidas", "tiktok", "publication", 500, 9.90, 8},
		{"1.000 curtidas TikTok", "Visibilidade", "curtidas", "tiktok", "publication", 1000, 19.90, 9},
		{"5.000 curtidas TikTok", "Trending", "curtidas", "tiktok", "publication", 5000, 79.90, 10},
		{"10.000 curtidas TikTok", "Para você", "curtidas", "tiktok", "publication", 10000, 129.90, 11},
		{"50.000 curtidas TikTok", "Viral", "curtidas", "tiktok", "publication", 50000, 549.90, 12},
		{"100.000 curtidas TikTok", "Explosão", "curtidas", "tiktok", "publication", 100000, 899.90, 13},

		// ---- Views TikTok (publicação) ----
		{"10.000 views em vídeo TikTok", "Pickup", "visualizacoes", "tiktok", "publication", 10000, 9.90, 3},
		{"100.000 views em vídeo TikTok", "Trending", "visualizacoes", "tiktok", "publication", 100000, 59.90, 4},
		{"1.000.000 views em vídeo TikTok", "Viral", "visualizacoes", "tiktok", "publication", 1000000, 399.90, 5},

		// ---- Comentários TikTok (publicação) ----
		{"100 comentários TikTok", "Conversa", "comentarios", "tiktok", "publication", 100, 29.90, 6},
		{"500 comentários TikTok", "Discussão", "comentarios", "tiktok", "publication", 500, 119.90, 7},

		// ---- Compartilhamentos TikTok (publicação) ----
		{"500 compartilhamentos TikTok", "Reach extra", "compartilhamentos", "tiktok", "publication", 500, 49.90, 5},
		{"5.000 compartilhamentos TikTok", "Viralidade", "compartilhamentos", "tiktok", "publication", 5000, 349.90, 6},

		// ===== Visualizações IG legacy =====
		{"Views 10k", "10.000 visualizações em posts", "visualizacoes", "instagram", "publication", 10000, 12.90, 1},
		{"Views 50k", "50.000 visualizações em posts", "visualizacoes", "instagram", "publication", 50000, 49.90, 2},

		// ===== Engajamento combinado IG =====
		{"Pacote engajamento 1k", "1k curtidas + 50 coments", "engajamento", "instagram", "publication", 1000, 39.90, 1},
		{"Pacote engajamento 5k", "5k curtidas + 200 coments + 500 shares", "engajamento", "instagram", "publication", 5000, 159.90, 2},

		// ===== Serviços (consultoria — perfil, multi-plataforma) =====
		{"Auditoria de perfil", "Diagnóstico + recomendações", "servicos", "instagram", "profile", 1, 149.90, 1},
		{"Gestão Mensal", "Gestão de perfil + estratégia", "servicos", "instagram", "profile", 1, 299.90, 2},
		{"Lançamento de produto", "Campanha integrada em 30 dias", "servicos", "instagram", "profile", 1, 1499.90, 3},
	}
	for _, p := range plans {
		var existingID string
		_ = db.pool.QueryRow(ctx,
			`SELECT id FROM plans WHERE name=$1 AND category=$2 LIMIT 1`,
			p.name, p.category).Scan(&existingID)
		if existingID != "" {
			// Já existe — só atualiza platform/target_type pra refletir o seed atual.
			_, _ = db.pool.Exec(ctx, `UPDATE plans SET platform=$2, target_type=$3 WHERE id=$1`,
				existingID, p.platform, p.target)
			continue
		}
		id := uuid.New().String()
		cents := int(p.brl*100 + 0.5)
		_, err := db.pool.Exec(ctx, `
			INSERT INTO plans (id, name, description, category, platform, target_type, followers_qty, price_cents, currency, active, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'BRL',true,$9)`,
			id, p.name, p.desc, p.category, p.platform, p.target, p.qty, cents, p.order)
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
	gws := []struct {
		name, provider, config string
		active                 bool
	}{
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
