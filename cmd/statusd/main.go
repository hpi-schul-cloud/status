package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hpi-schul-cloud/status/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	databaseURL, err := databaseURLFromEnv()
	if err != nil {
		logger.Error("database configuration", "error", err)
		os.Exit(1)
	}

	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		logger.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}
	seedConfig, err := store.SeedConfigFromEnv()
	if err != nil {
		logger.Error("read initial configuration", "error", err)
		os.Exit(1)
	}
	if err := db.Seed(ctx, seedConfig); err != nil {
		logger.Error("seed initial configuration", "error", err)
		os.Exit(1)
	}

	app, err := newApplication(db, logger)
	if err != nil {
		logger.Error("configure application", "error", err)
		os.Exit(1)
	}

	addr := envOr("LISTEN_ADDR", ":8080")
	server := &http.Server{
		Addr:              addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown http server", "error", err)
		}
	}()

	logger.Info("status service listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server stopped", "error", err)
		os.Exit(1)
	}
}

func databaseURLFromEnv() (string, error) {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		return dsn, nil
	}
	host := strings.TrimSpace(os.Getenv("DATABASE_HOST"))
	port := strings.TrimSpace(os.Getenv("DATABASE_PORT"))
	name := strings.TrimSpace(os.Getenv("DATABASE_NAME"))
	user := os.Getenv("DATABASE_USER")
	password := os.Getenv("DATABASE_PASSWORD")
	if host == "" || port == "" || name == "" || user == "" || password == "" {
		return "", errors.New("DATABASE_URL or complete DATABASE_HOST, DATABASE_PORT, DATABASE_NAME, DATABASE_USER, and DATABASE_PASSWORD settings are required")
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + name,
	}
	query := url.Values{}
	query.Set("sslmode", "disable")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
