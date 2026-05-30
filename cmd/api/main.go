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
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/email"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/payment"
	"github.com/viralefy/viralefy_api/internal/infrastructure/persistence/postgres"
	httphandler "github.com/viralefy/viralefy_api/internal/interface/http"
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
	roleRepo := postgres.NewRoleRepo(db)
	categoryRepo := postgres.NewCategoryRepo(db)
	currencyRepo := postgres.NewCurrencyRepo(db)
	ticketRepo := postgres.NewTicketRepo(db)
	profileRepo := postgres.NewProfileRepo(db)
	creditRepo := postgres.NewCreditRepo(db)
	invoiceRepo := postgres.NewInvoiceRepo(db)

	emailSender := email.New(email.Config{
		Provider:       cfg.EmailProvider,
		Addr:           cfg.SMTPAddr,
		User:           cfg.SMTPUser,
		Pass:           cfg.SMTPPass,
		From:           cfg.SMTPFrom,
		FromName:       cfg.SMTPFromName,
		ResendAPIKey:   cfg.ResendAPIKey,
		ResendFrom:     cfg.ResendFrom,
		ResendFromName: cfg.ResendFromName,
		ResendBaseURL:  cfg.ResendBaseURL,
	})

	payments := application.NewPaymentRegistry(
		payment.NewWoovi(),
		payment.NewHeleket(),
		payment.NewManualPIX(),
	)

	planSvc := application.NewPlanService(planRepo)
	currencySvc := application.NewCurrencyService(currencyRepo)
	creditSvc := application.NewCreditService(creditRepo)
	profileSvc := application.NewProfileService(profileRepo)
	invoiceSvc := application.NewInvoiceService(invoiceRepo, gwRepo, userRepo, creditSvc, currencySvc, payments)
	checkoutSvc := application.NewCheckoutService(userRepo, planRepo, orderRepo, gwRepo, profileRepo, currencySvc, creditSvc, emailSender, payments, cfg.SiteURL)
	gwSvc := application.NewGatewayService(gwRepo)
	authSvc := application.NewAuthService(adminRepo, roleRepo, cfg.JWTSecret, cfg.JWTTTL)
	userAuthSvc := application.NewUserAuthService(userRepo, cfg.JWTSecret, cfg.JWTTTL)
	ticketSvc := application.NewTicketService(ticketRepo, userRepo, emailSender, cfg.SiteURL)
	paymentReceiver := application.NewPaymentReceiver(invoiceRepo, orderRepo, invoiceSvc)

	h := &httphandler.Handlers{
		Plans:           planSvc,
		Checkout:        checkoutSvc,
		Gateways:        gwSvc,
		Auth:            authSvc,
		UserAuth:        userAuthSvc,
		Currencies:      currencySvc,
		Categories:      categoryRepo,
		Orders:          orderRepo,
		Users:           userRepo,
		Tickets:         ticketSvc,
		Profiles:        profileSvc,
		Credits:         creditSvc,
		Invoices:        invoiceSvc,
		PaymentReceiver: paymentReceiver,
	}

	router := httphandler.NewRouter(h, cfg.CORSOrigins,
		httphandler.AdminAuth(authSvc),
		httphandler.UserAuth(userAuthSvc),
		httphandler.OptionalUserAuth(userAuthSvc),
	)
	addr := cfg.BindHost + ":" + cfg.Port
	srv := &http.Server{Addr: addr, Handler: router}

	go func() {
		log.Printf("viralefy_api listening on %s", addr)
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
