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

	"github.com/viharam/go-api-backend/internal/app"
	"github.com/viharam/go-api-backend/internal/config"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var db *database.Postgres
	if cfg.DatabaseURL != "" {
		db, err = database.Connect(ctx, cfg)
		if err != nil {
			log.Error("postgres connection failed", "error", err)
			os.Exit(1)
		}
		defer db.Close()
	}
	var mongoDB *mongodb.Database
	if cfg.MongoURI != "" {
		mongoDB, err = mongodb.Connect(ctx, cfg.MongoURI, cfg.MongoDatabase)
		if err != nil {
			log.Error("mongodb connection failed", "error", err)
			os.Exit(1)
		}
		defer mongoDB.Close(context.Background())
	}

	engine := app.New(cfg, log, db, mongoDB)
	server := &http.Server{Addr: cfg.Address(), Handler: engine, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("server shutdown failed", "error", err)
		}
	}()

	log.Info("Viharam API listening", "address", cfg.Address())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}
