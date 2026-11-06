package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/app"
	"github.com/viharam/go-api-backend/internal/config"
	"github.com/viharam/go-api-backend/internal/platform/database"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	cfg := &config.Config{
		JWTSecret:          "route-inventory-test-secret-that-is-long-enough",
		JWTExpiresIn:       time.Hour,
		CORSAllowedOrigins: []string{"*"},
	}
	engine := app.New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)), &database.Postgres{}, nil)
	routes := make([]string, 0, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes = append(routes, route.Method+" "+route.Path)
	}
	sort.Strings(routes)
	if err := json.NewEncoder(os.Stdout).Encode(routes); err != nil {
		panic(err)
	}
}
