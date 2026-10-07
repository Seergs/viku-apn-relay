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

	"github.com/Seergs/viku-apn-relay/internal/apns"
	"github.com/Seergs/viku-apn-relay/internal/push"
	"github.com/Seergs/viku-apn-relay/internal/registration"
	"github.com/Seergs/viku-apn-relay/internal/server"
	"github.com/Seergs/viku-apn-relay/internal/webhook"
)

const (
	shutdownTimeout = 10 * time.Second
	defaultBaseURL  = "https://relay.viku.dev"
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

	apnsClient, err := newAPNsClient()
	if err != nil {
		return err
	}

	store, err := registration.Open(envOr("DATABASE_PATH", defaultDBPath))
	if err != nil {
		return err
	}
	defer store.Close()

	api := registration.NewAPI(store, envOr("PUBLIC_BASE_URL", defaultBaseURL))
	dispatcher := push.NewDispatcher(apnsClient, store, logger)
	webhooks := webhook.NewHandler(store, dispatcher)

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

// newAPNsClient reads the Apple credentials from the environment. The relay
// does not start without them, so it never runs in a mode that drops pushes
// silently.
func newAPNsClient() (*apns.Client, error) {
	keyPEM, err := loadAPNsKey()
	if err != nil {
		return nil, err
	}
	return apns.NewClient(apns.Config{
		KeyID:    os.Getenv("APNS_KEY_ID"),
		TeamID:   os.Getenv("APNS_TEAM_ID"),
		Topic:    os.Getenv("APNS_TOPIC"),
		KeyPEM:   keyPEM,
		Endpoint: envOr("APNS_ENDPOINT", apns.ProductionEndpoint),
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadAPNsKey returns the PEM-encoded APNs key. APNS_PRIVATE_KEY_FILE names a
// file, which is how Docker secrets are mounted. APNS_PRIVATE_KEY carries the
// key inline and is the fallback.
func loadAPNsKey() ([]byte, error) {
	if path := os.Getenv("APNS_PRIVATE_KEY_FILE"); path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("read APNS_PRIVATE_KEY_FILE")
		}
		return key, nil
	}
	if key := os.Getenv("APNS_PRIVATE_KEY"); key != "" {
		return []byte(key), nil
	}
	return nil, errors.New("APNS_PRIVATE_KEY_FILE or APNS_PRIVATE_KEY must be set")
}
