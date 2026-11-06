package uploads

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/middleware"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0}, 64)...)

func newEngine(t *testing.T, maxBytes int64) (*gin.Engine, *Handler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewHandler(t.TempDir(), maxBytes, "")
	engine := gin.New()
	engine.POST("/api/v1/uploads", func(c *gin.Context) {
		c.Set("claims", &middleware.Claims{UserID: "user-1", Role: "Host"})
		c.Next()
	}, h.Upload)
	h.Serve(engine)
	return engine, h
}

func multipartBody(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

func post(engine *gin.Engine, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", body)
	req.Header.Set("Content-Type", contentType)
	req.Host = "api.example.test"
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestUploadStoresImageAndServesIt(t *testing.T) {
	engine, h := newEngine(t, 0)
	body, ct := multipartBody(t, "file", "../../evil name.jpg", pngBytes) // client name is ignored
	rec := post(engine, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Success bool `json:"success"`
		Data    struct {
			URL      string `json:"url"`
			Path     string `json:"path"`
			MimeType string `json:"mimeType"`
			Size     int64  `json:"size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Data.MimeType != "image/png" || res.Data.Size != int64(len(pngBytes)) {
		t.Fatalf("response = %+v", res)
	}
	if !strings.HasPrefix(res.Data.URL, "http://api.example.test/uploads/") || !strings.HasSuffix(res.Data.URL, ".png") {
		t.Errorf("url = %s", res.Data.URL)
	}
	stored := filepath.Join(h.Dir(), filepath.Base(res.Data.Path))
	if data, err := os.ReadFile(stored); err != nil || !bytes.Equal(data, pngBytes) {
		t.Fatalf("stored file mismatch: %v", err)
	}

	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest(http.MethodGet, res.Data.Path, nil))
	if get.Code != http.StatusOK || get.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("GET %s = %d headers=%v", res.Data.Path, get.Code, get.Header())
	}
	listing := httptest.NewRecorder()
	engine.ServeHTTP(listing, httptest.NewRequest(http.MethodGet, "/uploads/", nil))
	if strings.Contains(listing.Body.String(), filepath.Base(res.Data.Path)) {
		t.Error("directory listing must be disabled")
	}
}

func TestUploadRejectsDisguisedHTML(t *testing.T) {
	engine, _ := newEngine(t, 0)
	body, ct := multipartBody(t, "file", "photo.png", []byte("<html><script>alert(1)</script></html>"))
	if rec := post(engine, body, ct); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestUploadRejectsOversizeFile(t *testing.T) {
	engine, _ := newEngine(t, 1024)
	big := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{1}, 4096)...)
	body, ct := multipartBody(t, "file", "big.png", big)
	if rec := post(engine, body, ct); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestUploadRequiresFileField(t *testing.T) {
	engine, _ := newEngine(t, 0)
	body, ct := multipartBody(t, "other", "a.png", pngBytes)
	if rec := post(engine, body, ct); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", strings.NewReader(`{"file":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("json body status = %d, want 400", rec.Code)
	}
}

func TestUploadPrefersConfiguredPublicBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(t.TempDir(), 0, "https://cdn.viharam.in/")
	engine := gin.New()
	engine.POST("/u", func(c *gin.Context) { c.Set("claims", &middleware.Claims{UserID: "u"}) }, h.Upload)
	body, ct := multipartBody(t, "file", "a.png", pngBytes)
	req := httptest.NewRequest(http.MethodPost, "/u", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"url":"https://cdn.viharam.in/uploads/`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
