package database

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/viharam/go-api-backend/internal/config"
)

type Postgres struct{ Pool *pgxpool.Pool }

func Connect(ctx context.Context, cfg *config.Config) (*Postgres, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	// Supabase's transaction pooler does not guarantee connection reuse for
	// prepared statements. Execute queries directly instead of using pgx's
	// connection-local prepared-statement cache.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	poolCfg.MaxConns = 20
	// Keep one connection warm so the first catalogue request after a quiet
	// period does not pay the Supabase connection setup cost.
	poolCfg.MinConns = 1
	poolCfg.MaxConnIdleTime = 10 * time.Minute
	poolCfg.ConnConfig.ConnectTimeout = 10 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{Pool: pool}, nil
}

func (p *Postgres) Ping(ctx context.Context) error { return p.Pool.Ping(ctx) }
func (p *Postgres) Close()                         { p.Pool.Close() }
