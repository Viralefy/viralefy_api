package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/viralefy/viralefy_api/internal/application"
	"github.com/viralefy/viralefy_api/internal/config"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/email"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/notify"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/payment"
	"github.com/viralefy/viralefy_api/internal/infrastructure/external/turnstile"
	"github.com/viralefy/viralefy_api/internal/infrastructure/observability"
	"github.com/viralefy/viralefy_api/internal/infrastructure/persistence/postgres"
	httphandler "github.com/viralefy/viralefy_api/internal/interface/http"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	// ---- Observabilidade (logger, métricas, traces) ---- //
	// Versão é injetada via -ldflags em release builds; default "dev" em local.
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}
	logger := observability.InitLogger(observability.LoggerConfig{
		Level:     slog.LevelInfo,
		Service:   "viralefy-api",
		Version:   version,
		Component: "api",
	})
	observability.InitMetrics()

	tracerCtx, tracerCancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownTracer, err := observability.InitTracer(tracerCtx, observability.TracingConfig{
		ServiceName:    "viralefy-api",
		ServiceVersion: version,
		Environment:    os.Getenv("APP_ENV"),
	})
	tracerCancel()
	if err != nil {
		// Tracing é não-bloqueante: se Tempo não estiver pronto, só logamos e
		// seguimos sem traces. A métrica/log ainda capturam tudo.
		logger.Warn("tracer init failed; continuing without traces", "error", err.Error())
		shutdownTracer = func(context.Context) error { return nil }
	}

	ctx := context.Background()
	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connect failed", "error", err.Error())
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
	notifier := notify.NewWebhookClient(cfg.AdminWebhookURL)
	if !notifier.Enabled() {
		logger.Warn("admin webhook disabled (ADMIN_WEBHOOK_URL empty)")
	}
	paymentReceiver := application.NewPaymentReceiver(
		invoiceRepo, orderRepo, planRepo, userRepo,
		ticketSvc, invoiceSvc, emailSender, notifier, cfg.SiteURL,
	)

	auditRepo := postgres.NewAuditRepo(db)
	auditSvc := application.NewAuditService(auditRepo)

	turnstileSvc := turnstile.NewService(cfg.TurnstileSecretKey)
	if !turnstileSvc.Enabled() {
		logger.Warn("turnstile disabled (TURNSTILE_SECRET_KEY empty) — anti-bot bypass")
	}

	metricCaptureSvc := application.NewMetricCaptureService(orderRepo, planRepo, profileRepo)
	// Plumba pro CheckoutService disparar baseline async no momento da
	// criação do pedido. Ver checkout_service.SetMetricCapture.
	checkoutSvc.SetMetricCapture(metricCaptureSvc)

	// Cron de delivery capture: 24h pós-pago, tira snapshot da 2ª fonte de
	// verdade (perfil/post público) e grava em orders.delivery_metrics.
	// Substitui o fluxo manual de admin clicar "Capturar delivery agora" em
	// cada pedido. Intervalo 15min, batch 25 — config padrão tunada pra HML.
	deliveryCron := &application.DeliveryCaptureCron{
		Orders:  orderRepo,
		Metrics: metricCaptureSvc,
	}
	deliveryCron.Start(context.Background())

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
		Turnstile:       turnstileSvc,
		Audit:           auditSvc,
		DB:              db,
		Metrics:         metricCaptureSvc,
	}

	// /ready faz Ping no pool — falha vira 503 (drena tráfego no rolling update).
	readyCheck := httphandler.ReadyChecker(func(r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		return db.Pool().Ping(ctx)
	})

	router := httphandler.NewRouter(h, cfg.CORSOrigins, readyCheck,
		httphandler.AdminAuth(authSvc),
		httphandler.UserAuth(userAuthSvc),
		httphandler.OptionalUserAuth(userAuthSvc),
	)
	addr := cfg.BindHost + ":" + cfg.Port
	srv := &http.Server{Addr: addr, Handler: router}

	go func() {
		logger.Info("viralefy_api listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("listen failed", "error", err.Error())
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = shutdownTracer(shutdownCtx)
}
