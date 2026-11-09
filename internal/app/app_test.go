package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/viharam/go-api-backend/internal/config"
	"github.com/viharam/go-api-backend/internal/middleware"
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

func TestHealthHeadRoute(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-long-enough-for-the-router", JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	engine := New(cfg, slog.Default(), nil, &mongodb.Database{})

	head := httptest.NewRecorder()
	engine.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/getHealthHead", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD /getHealthHead = %d, want %d", head.Code, http.StatusOK)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD /getHealthHead returned a body: %q", head.Body.String())
	}

	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/getHealthHead", nil))
	if get.Code != http.StatusNotFound {
		t.Fatalf("GET /getHealthHead = %d, want %d", get.Code, http.StatusNotFound)
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

const portalTestSecret = "test-secret-that-is-long-enough-for-the-router"

func portalToken(t *testing.T, role string) string {
	t.Helper()
	claims := middleware.Claims{UserID: "11111111-1111-1111-1111-111111111111", Email: "t@example.com", Role: role,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(portalTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestMobilePortalRoutesAreRegistered(t *testing.T) {
	cfg := &config.Config{JWTSecret: portalTestSecret, JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	want := map[string]bool{
		"GET /api/v1/host/listings":                              false,
		"GET /api/v1/host/earnings":                              false,
		"GET /api/v1/ambassador/earnings":                        false,
		"GET /api/v1/ambassador/hosts":                           false,
		"GET /api/v1/ambassador-assignments/:id/guests":          false,
		"POST /api/v1/ambassador-assignments/:id/message-guests": false,
		"GET /api/v1/ambassador-assignments/:id/messages":        false,
		"POST /api/v1/ambassador/visits":                         false,
		"GET /api/v1/ambassador/visits":                          false,
		"PATCH /api/v1/ambassador/visits/:id":                    false,
		"POST /api/v1/uploads":                                   false,
	}
	for _, route := range New(cfg, slog.Default(), &database.Postgres{}, nil).Routes() {
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

func TestUploadRouteExistsInMongoCompatibilityMode(t *testing.T) {
	cfg := &config.Config{JWTSecret: portalTestSecret, JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	for _, route := range New(cfg, slog.Default(), nil, &mongodb.Database{}).Routes() {
		if route.Method == "POST" && route.Path == "/api/v1/uploads" {
			return
		}
	}
	t.Fatal("POST /api/v1/uploads not registered")
}

// The role and auth checks run before any database access, so they can be
// exercised without a database.
func TestMobilePortalRoutesEnforceAuthAndRole(t *testing.T) {
	cfg := &config.Config{JWTSecret: portalTestSecret, JWTExpiresIn: time.Hour, CORSAllowedOrigins: []string{"*"}}
	engine := New(cfg, slog.Default(), &database.Postgres{}, nil)
	cases := []struct {
		path, wrongRole string
	}{
		{"/api/v1/host/listings", "Traveller"},
		{"/api/v1/host/earnings", "Ambassador"},
		{"/api/v1/ambassador/earnings", "Host"},
		{"/api/v1/ambassador/hosts", "Traveller"},
	}
	for _, tc := range cases {
		anon := httptest.NewRecorder()
		engine.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if anon.Code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", tc.path, anon.Code)
		}
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+portalToken(t, tc.wrongRole))
		wrong := httptest.NewRecorder()
		engine.ServeHTTP(wrong, req)
		if wrong.Code != http.StatusForbidden {
			t.Errorf("%s as %s = %d, want 403", tc.path, tc.wrongRole, wrong.Code)
		}
	}
	for _, path := range []string{"/api/v1/ambassador-assignments/x/guests"} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", path, rec.Code)
		}
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/ambassador/visits"},
		{http.MethodPost, "/api/v1/ambassador/visits"},
		{http.MethodPatch, "/api/v1/ambassador/visits/x"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+portalToken(t, "Traveller"))
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as Traveller = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/ambassador-assignments/x/message-guests"},
		{http.MethodGet, "/api/v1/ambassador-assignments/x/messages"},
	} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
	upload := httptest.NewRecorder()
	engine.ServeHTTP(upload, httptest.NewRequest(http.MethodPost, "/api/v1/uploads", nil))
	if upload.Code != http.StatusUnauthorized {
		t.Errorf("upload without token = %d, want 401", upload.Code)
	}
}
