// Command gitstate is the main API + admin server.
// It loads config, opens the DB pool (with a warning-not-fatal if unreachable),
// builds the HTTP router, and serves with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exo/gitstate/internal/api"
	"github.com/exo/gitstate/internal/billing"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/exchange"
	"github.com/exo/gitstate/internal/invoicedelivery"
	"github.com/exo/gitstate/internal/jobs"
)

func main() {
	// Structured logging to stdout.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Load configuration (file + env overlay).
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("starting gitstate",
		"env", cfg.App.Env,
		"addr", cfg.App.HTTPAddr,
		"public_url", cfg.App.PublicURL,
	)

	ctx := context.Background()

	// Open DB pool — log a warning but still boot if unreachable (dev convenience).
	var database *db.DB
	database, err = db.New(ctx, cfg)
	if err != nil {
		slog.Warn("database unavailable — starting without DB", "error", err)
	} else {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if pingErr := database.Ping(pingCtx); pingErr != nil {
			slog.Warn("database ping failed — starting without confirmed DB connection", "error", pingErr)
		} else {
			slog.Info("database connected")
		}
		cancel()
	}

	// Start the USD↔ZAR exchange-rate refresher when a DB and a provider key are present.
	if database != nil && cfg.Billing.Exchange.APIKey != "" {
		exchange.New(database, cfg).StartRefresher(ctx)
		slog.Info("exchange-rate refresher started", "provider", cfg.Billing.Exchange.Provider)
	}

	// Durable background job queue (repo syncs survive restarts). Prefers the
	// BYPASSRLS admin pool (ADMIN_DATABASE_URL) for cross-org dequeue. SetJobQueue
	// MUST run before api.NewRouter (which calls RegisterSyncRoutes, reading it).
	if database != nil {
		queue, qerr := jobs.New(database, cfg)
		if qerr != nil {
			slog.Error("failed to start job queue", "error", qerr)
			os.Exit(1)
		}
		api.RegisterSyncJobHandlers(queue, database, cfg) // register BEFORE Start
		api.SetJobQueue(queue)                            // inject into sync handlers (BEFORE NewRouter)
		queue.Start(ctx)                                  // RequeueStale + workers + stale ticker
		defer queue.Close()
		slog.Info("job queue started")
	}

	// Billing lifecycle: monthly scheduler + dunning machine. Issued invoices are
	// emailed (PDF) to org owners via the decoupled hook. The Charger stays nil
	// until the Paystack gateway phase passes an EE charger (real balances enter
	// dunning, $0 invoices settle).
	if database != nil && cfg.Billing.Enabled {
		billing.InvoiceEmailHook = func(ctx context.Context, orgID, invoiceID string) error {
			return invoicedelivery.EmailInvoiceToOwners(ctx, database, cfg, orgID, invoiceID)
		}
		sched, serr := billing.StartBillingScheduler(ctx, billing.SystemClock{}, database, cfg, nil)
		if serr != nil {
			slog.Error("failed to start billing scheduler", "error", serr)
		} else {
			defer sched.Close()
			slog.Info("billing scheduler started")
		}
	}

	// Build router with middleware wired.
	handler := api.NewRouter(cfg, database)

	addr := cfg.App.HTTPAddr
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in a goroutine so we can listen for signals.
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Wait for a shutdown signal or a server error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		slog.Info("shutdown signal received", "signal", sig)
	case err := <-serverErr:
		if err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
		return
	}

	// Graceful shutdown: give in-flight requests 30 seconds to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}

	// Close DB pool after all HTTP connections are done.
	if database != nil {
		database.Close()
		slog.Info("database pool closed")
	}

	slog.Info("gitstate stopped")
}
