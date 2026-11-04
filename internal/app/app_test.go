package app

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/viharam/go-api-backend/internal/config"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
)

func TestMongoRoutesRegisterWithoutConflicts(t *testing.T) {
	cfg := &config.Config{
		JWTSecret:          "test-secret-that-is-long-enough-for-the-router",
		JWTExpiresIn:       time.Hour,
		CORSAllowedOrigins: []string{"*"},
	}
	engine := New(cfg, slog.Default(), nil, &mongodb.Database{})
	routes := engine.Routes()
	want := map[string]bool{
		"POST /api/v1/auth/register":                      false,
		"GET /api/v1/properties/:id":                      false,
		"POST /api/v1/bookings":                           false,
		"POST /api/v1/experience-bookings/lock":           false,
		"DELETE /api/v1/experience-bookings/lock/:lockId": false,
		"POST /api/v1/experience-bookings/:id/cancel":     false,
	}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("route not registered: %s", route)
		}
	}
}

func TestSupabaseRoutesRegisterWithoutMongo(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-long-enough-for-the-router", JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	engine := New(cfg, slog.Default(), &database.Postgres{}, nil)
	routes := engine.Routes()
	want := map[string]bool{
		"GET /api/v1/categories":        false,
		"GET /api/v1/experiences/:id":   false,
		"GET /api/v1/properties":        false,
		"POST /api/v1/properties":       false,
		"GET /api/v1/bookings":          false,
		"POST /api/v1/bookings":         false,
		"GET /api/v1/host-profiles":     false,
		"POST /api/v1/support-tickets":  false,
		"POST /api/v1/coupons/validate": false,
	}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("Supabase route not registered: %s", route)
		}
	}
}

func TestSupabaseWinsWhenMongoCompatibilityIsAlsoConfigured(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-long-enough-for-the-router", JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	engine := New(cfg, slog.Default(), &database.Postgres{}, &mongodb.Database{})
	found := false
	for _, route := range engine.Routes() {
		if route.Method == "GET" && route.Path == "/api/v1/host-profiles" {
			found = true
			if !strings.Contains(route.Handler, "hostprofilespg") {
				t.Fatalf("expected Supabase document handler, got %s", route.Handler)
			}
		}
	}
	if !found {
		t.Fatal("Supabase host-profile route was not registered")
	}
}

func TestSupabaseRuntimeHasNoMongoBackedAPIHandlers(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-long-enough-for-the-router", JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	engine := New(cfg, slog.Default(), &database.Postgres{}, nil)
	for _, route := range engine.Routes() {
		if strings.Contains(route.Handler, "mongodb") || strings.Contains(route.Handler, "simplecrud") || strings.Contains(route.Handler, "commerce") || strings.Contains(route.Handler, "support.(*Handler)") || strings.Contains(route.Handler, "incidents.(*Handler)") {
			t.Errorf("Supabase route uses compatibility handler: %s %s -> %s", route.Method, route.Path, route.Handler)
		}
	}
}
