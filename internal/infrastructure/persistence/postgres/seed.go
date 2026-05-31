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
		{"seguidores", "Followers", 1},
		{"engajamento", "Engagement", 2},
		{"visualizacoes", "Views", 3},
		{"servicos", "Premium services", 4},
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
	// Idempotent by natural-key tuple (category, platform, target_type, followers_qty).
	// Re-runs refresh name/description/price/sort_order so the seed stays authoritative
	// even if labels evolve; manual per-currency overrides on plan_prices are preserved.
	type planSeed struct {
		name, desc, category, platform, target string
		qty                                    int
		brl                                    float64
		order                                  int
	}
	plans := []planSeed{
		// ===== INSTAGRAM ===== //
		// ---- Followers (profile) — IG sort_order 1-13 ----
		{"100 followers Instagram", "Ideal for testing", "seguidores", "instagram", "profile", 100, 9.90, 1},
		{"250 followers Instagram", "First push", "seguidores", "instagram", "profile", 250, 18.90, 2},
		{"500 followers Instagram", "Initial growth", "seguidores", "instagram", "profile", 500, 35.90, 3},
		{"1,000 followers Instagram", "More reach", "seguidores", "instagram", "profile", 1000, 69.90, 4},
		{"2,500 followers Instagram", "Traction", "seguidores", "instagram", "profile", 2500, 149.90, 5},
		{"5,000 followers Instagram", "Community", "seguidores", "instagram", "profile", 5000, 249.90, 6},
		{"10,000 followers Instagram", "Scale", "seguidores", "instagram", "profile", 10000, 399.90, 7},
		{"25,000 followers Instagram", "Micro-influencer", "seguidores", "instagram", "profile", 25000, 899.90, 8},
		{"50,000 followers Instagram", "Established influence", "seguidores", "instagram", "profile", 50000, 1699.90, 9},
		{"100,000 followers Instagram", "Authority", "seguidores", "instagram", "profile", 100000, 3199.90, 10},
		{"250,000 followers Instagram", "Personal brand", "seguidores", "instagram", "profile", 250000, 6499.90, 11},
		{"500,000 followers Instagram", "Top tier", "seguidores", "instagram", "profile", 500000, 8999.90, 12},
		{"1,000,000 followers Instagram", "Maximum reach", "seguidores", "instagram", "profile", 1000000, 11999.99, 13},

		// ---- Followers TikTok (profile) — sort_order 100-107 ----
		{"500 followers TikTok", "First push", "seguidores", "tiktok", "profile", 500, 29.90, 100},
		{"1,000 followers TikTok", "Takeoff", "seguidores", "tiktok", "profile", 1000, 54.90, 101},
		{"5,000 followers TikTok", "Solid growth", "seguidores", "tiktok", "profile", 5000, 199.90, 102},
		{"10,000 followers TikTok", "TikTok scale", "seguidores", "tiktok", "profile", 10000, 349.90, 103},
		{"50,000 followers TikTok", "Creator", "seguidores", "tiktok", "profile", 50000, 1499.90, 104},
		{"100,000 followers TikTok", "TikTok influence", "seguidores", "tiktok", "profile", 100000, 2799.90, 105},
		{"500,000 followers TikTok", "Top creator", "seguidores", "tiktok", "profile", 500000, 7999.90, 106},
		{"1,000,000 followers TikTok", "Full viral", "seguidores", "tiktok", "profile", 1000000, 10999.90, 107},

		// ===== ENGAGEMENT ===== //
		// ---- IG likes (publication) — sort_order 1-7 ----
		{"100 likes Instagram", "Initial boost", "engajamento", "instagram", "publication", 100, 4.90, 1},
		{"500 likes Instagram", "Average engagement", "engajamento", "instagram", "publication", 500, 14.90, 2},
		{"1,000 likes Instagram", "High visibility", "engajamento", "instagram", "publication", 1000, 24.90, 3},
		{"5,000 likes Instagram", "Going viral", "engajamento", "instagram", "publication", 5000, 89.90, 4},
		{"10,000 likes Instagram", "Trending", "engajamento", "instagram", "publication", 10000, 149.90, 5},
		{"50,000 likes Instagram", "Top of feed", "engajamento", "instagram", "publication", 50000, 599.90, 6},
		{"100,000 likes Instagram", "Explosive", "engajamento", "instagram", "publication", 100000, 999.90, 7},

		// ---- IG comments (publication) — sort_order 10-14 ----
		{"50 comments Instagram", "Conversation starter", "engajamento", "instagram", "publication", 50, 19.90, 10},
		{"100 comments Instagram", "Real engagement", "engajamento", "instagram", "publication", 100, 34.90, 11},
		{"250 comments Instagram", "Active discussion", "engajamento", "instagram", "publication", 250, 79.90, 12},
		{"500 comments Instagram", "Community", "engajamento", "instagram", "publication", 500, 149.90, 13},
		{"1,000 comments Instagram", "Viral", "engajamento", "instagram", "publication", 1000, 269.90, 14},

		// ---- IG shares (publication) — sort_order 20-23 ----
		{"100 shares Instagram", "Spread the word", "engajamento", "instagram", "publication", 100, 12.90, 20},
		{"500 shares Instagram", "Extra reach", "engajamento", "instagram", "publication", 500, 49.90, 21},
		{"1,000 shares Instagram", "Trending content", "engajamento", "instagram", "publication", 1000, 89.90, 22},
		{"5,000 shares Instagram", "Real virality", "engajamento", "instagram", "publication", 5000, 399.90, 23},

		// ---- IG saves (publication) — sort_order 30-33 ----
		{"100 saves Instagram", "Valuable content", "engajamento", "instagram", "publication", 100, 9.90, 30},
		{"500 saves Instagram", "Reference material", "engajamento", "instagram", "publication", 500, 39.90, 31},
		{"1,000 saves Instagram", "Top of mind", "engajamento", "instagram", "publication", 1000, 69.90, 32},
		{"5,000 saves Instagram", "Evergreen content", "engajamento", "instagram", "publication", 5000, 299.90, 33},

		// ---- TikTok likes (publication = video) — sort_order 40-45 ----
		{"500 likes TikTok", "Video boost", "engajamento", "tiktok", "publication", 500, 9.90, 40},
		{"1,000 likes TikTok", "Visibility", "engajamento", "tiktok", "publication", 1000, 19.90, 41},
		{"5,000 likes TikTok", "Trending", "engajamento", "tiktok", "publication", 5000, 79.90, 42},
		{"10,000 likes TikTok", "For You page", "engajamento", "tiktok", "publication", 10000, 129.90, 43},
		{"50,000 likes TikTok", "Viral", "engajamento", "tiktok", "publication", 50000, 549.90, 44},
		{"100,000 likes TikTok", "Explosive", "engajamento", "tiktok", "publication", 100000, 899.90, 45},

		// ---- TikTok comments (publication) — sort_order 50-51 ----
		{"100 comments TikTok", "Conversation", "engajamento", "tiktok", "publication", 100, 29.90, 50},
		{"500 comments TikTok", "Discussion", "engajamento", "tiktok", "publication", 500, 119.90, 51},

		// ---- TikTok shares (publication) — sort_order 60-61 ----
		{"500 shares TikTok", "Extra reach", "engajamento", "tiktok", "publication", 500, 49.90, 60},
		{"5,000 shares TikTok", "Virality", "engajamento", "tiktok", "publication", 5000, 349.90, 61},

		// ===== VIEWS ===== //
		// ---- IG Reels views (publication) — sort_order 1-6 ----
		{"1,000 Reels views Instagram", "Initial pickup", "visualizacoes", "instagram", "publication", 1000, 4.90, 1},
		{"10,000 Reels views Instagram", "Trending", "visualizacoes", "instagram", "publication", 10000, 12.90, 2},
		{"50,000 Reels views Instagram", "Hot", "visualizacoes", "instagram", "publication", 50000, 49.90, 3},
		{"100,000 Reels views Instagram", "Boom", "visualizacoes", "instagram", "publication", 100000, 89.90, 4},
		{"500,000 Reels views Instagram", "Viral", "visualizacoes", "instagram", "publication", 500000, 379.90, 5},
		{"1,000,000 Reels views Instagram", "National hit", "visualizacoes", "instagram", "publication", 1000000, 699.90, 6},

		// ---- IG Story views (profile — Stories aggregate per account) — sort_order 10-12 ----
		{"500 Story views Instagram", "Story boost", "visualizacoes", "instagram", "profile", 500, 6.90, 10},
		{"2,000 Story views Instagram", "High presence", "visualizacoes", "instagram", "profile", 2000, 19.90, 11},
		{"10,000 Story views Instagram", "Massive", "visualizacoes", "instagram", "profile", 10000, 79.90, 12},

		// ---- TikTok video views (publication) — sort_order 100-102 ----
		{"10,000 video views TikTok", "Pickup", "visualizacoes", "tiktok", "publication", 10000, 9.90, 100},
		{"100,000 video views TikTok", "Trending", "visualizacoes", "tiktok", "publication", 100000, 59.90, 101},
		{"1,000,000 video views TikTok", "Viral", "visualizacoes", "tiktok", "publication", 1000000, 399.90, 102},

		// ===== PREMIUM SERVICES (consulting — profile, multi-platform) =====
		{"Profile audit", "Diagnosis + recommendations", "servicos", "instagram", "profile", 1, 149.90, 1},
		{"Monthly management", "Profile management + strategy", "servicos", "instagram", "profile", 1, 299.90, 2},
		{"Product launch", "Integrated 30-day campaign", "servicos", "instagram", "profile", 1, 1499.90, 3},
		{"Account recovery", "Recovery support for suspended or hacked accounts", "servicos", "instagram", "profile", 1, 399.90, 4},
		{"New account setup", "Full setup and optimization for a new account", "servicos", "instagram", "profile", 1, 249.90, 5},
		{"Anti-shadowban package", "Shadowban diagnosis and removal plan", "servicos", "instagram", "profile", 1, 349.90, 6},
		{"Competitor analysis", "In-depth analysis of direct competitors", "servicos", "instagram", "profile", 1, 199.90, 7},
		{"Verification support", "Support to apply for the verified badge", "servicos", "instagram", "profile", 1, 599.90, 8},
	}
	for _, p := range plans {
		var existingID string
		// UPSERT por (category, name) — name é o identificador único do plano
		// dentro da categoria. Antes usávamos natural-key (category, platform,
		// target_type, qty) mas em engajamento likes/comments/shares/saves
		// no mesmo qty colidiam (todos publication). UNIQUE em (category, name)
		// é o equivalente físico na DB (plans_category_name_key).
		_ = db.pool.QueryRow(ctx,
			`SELECT id FROM plans WHERE category=$1 AND name=$2 LIMIT 1`,
			p.category, p.name).Scan(&existingID)
		cents := int(p.brl*100 + 0.5)
		if existingID != "" {
			// Refresh description/price/sort_order/platform/target_type. Name fica
			// como está (lookup key), mas o resto vira authoritativo do seed.
			_, _ = db.pool.Exec(ctx,
				`UPDATE plans SET description=$2, price_cents=$3, sort_order=$4,
				                  platform=$5, target_type=$6, followers_qty=$7
				 WHERE id=$1`,
				existingID, p.desc, cents, p.order, p.platform, p.target, p.qty)
			if err := seedPlanPrices(ctx, db, existingID, p.brl); err != nil {
				return err
			}
			continue
		}
		id := uuid.New().String()
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
