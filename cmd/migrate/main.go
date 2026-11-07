package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	databaseURL := strings.TrimSpace(os.Getenv("SUPABASE_DB_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if databaseURL == "" {
		log.Fatal("SUPABASE_DB_URL or DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			filename text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		log.Fatal(err)
	}

	entries, err := os.ReadDir("db/migrations")
	if err != nil {
		log.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		var applied bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
			log.Fatal(err)
		}
		if applied {
			fmt.Printf("skip %s\n", entry.Name())
			continue
		}

		sql, err := os.ReadFile(filepath.Join("db/migrations", entry.Name()))
		if err != nil {
			log.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(sql)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version, filename) VALUES ($1, $2)`, version, entry.Name())
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			log.Fatalf("migration %s failed: %v", entry.Name(), err)
		}
		if err = tx.Commit(ctx); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("applied %s\n", entry.Name())
	}
}
