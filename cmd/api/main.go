package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/config"
	httphandler "github.com/viralefy/viralefy_api/internal/interface/http"
	"github.com/viralefy/viralefy_api/internal/infrastructure/persistence/postgres"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal("database:", err)
	}
	defer db.Close()

	if err := postgres.RunMigrations(ctx, db); err != nil {
		log.Fatal("migrate:", err)
	}
	if err := postgres.Seed(ctx, db); err != nil {
		log.Fatal("seed:", err)
	}

	planRepo := postgres.NewPlanRepo(db)
	userRepo := postgres.NewUserRepo(db)
	orderRepo := postgres.NewOrderRepo(db)
	gwRepo := postgres.NewGatewayRepo(db)
	adminRepo := postgres.NewAdminRepo(db)

	planSvc := application.NewPlanService(planRepo)
	checkoutSvc := application.NewCheckoutService(userRepo, planRepo, orderRepo, gwRepo)
	gwSvc := application.NewGatewayService(gwRepo)
	authSvc := application.NewAuthService(adminRepo, cfg.JWTSecret, cfg.JWTTTL)

	h := &httphandler.Handlers{
		Plans:    planSvc,
		Checkout: checkoutSvc,
		Gateways: gwSvc,
		Auth:     authSvc,
		Orders:   orderRepo,
	}

	router := httphandler.NewRouter(h, cfg.CORSOrigins, httphandler.AdminAuth(authSvc))
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: router}

	go func() {
		log.Printf("viralefy_api listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
