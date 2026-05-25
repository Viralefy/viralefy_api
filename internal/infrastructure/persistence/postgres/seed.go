package postgres

import (
	"context"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func Seed(ctx context.Context, db *DB) error {
	var n int
	_ = db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM plans`).Scan(&n)
	if n > 0 {
		return seedAdmin(ctx, db)
	}

	plans := []struct {
		name, desc string
		qty, cents, order int
	}{
		{"Starter", "Ideal para começar", 500, 1990, 1},
		{"Growth", "Crescimento rápido", 2000, 4990, 2},
		{"Pro", "Para creators sérios", 5000, 9990, 3},
		{"Elite", "Máximo alcance", 10000, 17990, 4},
	}
	for _, p := range plans {
		id := uuid.New().String()
		_, err := db.pool.Exec(ctx, `
			INSERT INTO plans (id, name, description, followers_qty, price_cents, currency, active, sort_order)
			VALUES ($1,$2,$3,$4,$5,'BRL',true,$6)`, id, p.name, p.desc, p.qty, p.cents, p.order)
		if err != nil {
			return err
		}
	}

	gwID := uuid.New().String()
	_, err := db.pool.Exec(ctx, `
		INSERT INTO payment_gateways (id, name, provider, active, config)
		VALUES ($1,'PIX Manual','manual_pix',true,'{"pix_key":"contato@viralefy.com"}')`, gwID)
	if err != nil {
		return err
	}

	return seedAdmin(ctx, db)
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
	id := uuid.New().String()
	_, err = db.pool.Exec(ctx, `
		INSERT INTO admins (id, email, password_hash, name)
		VALUES ($1,'admin@viralefy.local',$2,'Administrador')`, id, string(hash))
	return err
}
