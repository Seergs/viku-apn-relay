package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Seergs/viku-apn-relay/internal/registration"
	"github.com/Seergs/viku-apn-relay/internal/server"
	"github.com/Seergs/viku-apn-relay/internal/webhook"
)

const (
	shutdownTimeout = 10 * time.Second
	defaultBaseURL  = "https://relay.viku.app"
	defaultDBPath   = "relay.db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	addr := envOr("ADDR", ":8080")

	store, err := registration.Open(envOr("DATABASE_PATH", defaultDBPath))
	if err != nil {
		return err
	}
	defer store.Close()

	api := registration.NewAPI(store, envOr("PUBLIC_BASE_URL", defaultBaseURL))
	webhooks := webhook.NewHandler(store, webhook.DiscardDispatcher{})

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(webhooks, api),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
