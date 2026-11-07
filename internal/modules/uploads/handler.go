// Package uploads implements POST /api/v1/uploads: authenticated multipart
// uploads of images and PDFs. Supabase Storage is used when configured; local
// disk remains a development/test fallback.
package uploads

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/filetype"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

const (
	// DefaultDir is used when UPLOAD_DIR is not configured.
	DefaultDir = "./uploads"
	// DefaultMaxBytes is the per-file size limit (10 MiB).
	DefaultMaxBytes int64 = 10 << 20
	// multipartOverhead leaves room for form boundaries and small text fields.
	multipartOverhead int64 = 64 << 10
	// PublicPrefix is the URL path under which stored files are served.
	PublicPrefix = "/uploads"
)

var fieldNames = []string{"file", "image", "photo", "document"}

type Handler struct {
	dir      string
	maxBytes int64
	baseURL  string
	storage  *supabaseStorage
}

// NewHandler builds the upload handler. Empty/zero arguments select defaults.
// baseURL (PUBLIC_BASE_URL) overrides the scheme and host used in returned URLs.
func NewHandler(dir string, maxBytes int64, baseURL string) *Handler {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultDir
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Handler{dir: dir, maxBytes: maxBytes, baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/")}
}

// NewHandlerWithSupabase adds durable object storage without changing the
// local/test constructor used by the upload unit tests.
func NewHandlerWithSupabase(dir string, maxBytes int64, baseURL, supabaseURL, serviceKey, bucket string) *Handler {
	h := NewHandler(dir, maxBytes, baseURL)
	if strings.TrimSpace(supabaseURL) != "" && strings.TrimSpace(serviceKey) != "" && strings.TrimSpace(bucket) != "" {
		h.storage = &supabaseStorage{
			baseURL: strings.TrimRight(strings.TrimSpace(supabaseURL), "/"),
			serviceKey: strings.TrimSpace(serviceKey),
			bucket: strings.TrimSpace(bucket),
			client: &http.Client{Timeout: 45 * time.Second},
		}
	}
	return h
}

// Dir is the directory files are stored in (and must be served from).
func (h *Handler) Dir() string { return h.dir }

// Serve mounts the stored files read-only under /uploads: no directory
// listings, no content sniffing, and a locked-down Content-Security-Policy.
func (h *Handler) Serve(engine *gin.Engine) {
	group := engine.Group(PublicPrefix)
	group.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Next()
	})
	group.StaticFS("/", gin.Dir(h.dir, false))
}

// Upload handles POST /api/v1/uploads (multipart/form-data, field "file").
func (h *Handler) Upload(c *gin.Context) {
	if middleware.ClaimsFrom(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBytes+multipartOverhead)

	var (
		file   io.ReadSeeker
		closer io.Closer
		size   int64
		err    error
	)
	for _, name := range fieldNames {
		f, header, e := c.Request.FormFile(name)
		if e == nil {
			file, closer, size = f, f, header.Size
			err = nil
			break
		}
		err = e
		var tooBig *http.MaxBytesError
		if errors.As(e, &tooBig) {
			break
		}
	}
	if file == nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.tooLarge(c)
			return
		}
		_ = httpx.Fail(c, 400, "invalid_request", `Send multipart/form-data with a "file" field`)
		return
	}
	defer closer.Close()

	if size > h.maxBytes {
		h.tooLarge(c)
		return
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		_ = httpx.Fail(c, 400, "invalid_request", "Could not read the uploaded file")
		return
	}
	kind, ok := filetype.Classify(head[:n])
	if !ok {
		_ = httpx.Fail(c, 415, "unsupported_media_type", "Only JPEG, PNG, WebP images and PDF documents are allowed")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Could not read the uploaded file")
		return
	}

	name := uuid.NewString() + kind.Ext
	if h.storage != nil {
		if err := h.storage.put(c, name, kind.MIME, file, size); err != nil {
			_ = httpx.Fail(c, 502, "storage_error", "Could not store the file")
			return
		}
		path := PublicPrefix + "/" + name
		_ = httpx.Created(c, gin.H{
			"url":      h.storage.publicURL(name),
			"path":     path,
			"name":     name,
			"mimeType": kind.MIME,
			"size":     size,
		}, "File uploaded")
		return
	}

	if err := os.MkdirAll(h.dir, 0o750); err != nil {
		_ = httpx.Fail(c, 500, "storage_error", "Could not store the file")
		return
	}
	target := filepath.Join(h.dir, name)
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		_ = httpx.Fail(c, 500, "storage_error", "Could not store the file")
		return
	}
	written, copyErr := io.Copy(out, io.LimitReader(file, h.maxBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written > h.maxBytes {
		_ = os.Remove(target) // only ever removes the partial file created above
		if written > h.maxBytes {
			h.tooLarge(c)
			return
		}
		_ = httpx.Fail(c, 500, "storage_error", "Could not store the file")
		return
	}

	path := PublicPrefix + "/" + name
	_ = httpx.Created(c, gin.H{
		"url":      h.absoluteURL(c, path),
		"path":     path,
		"name":     name,
		"mimeType": kind.MIME,
		"size":     written,
	}, "File uploaded")
}

func (h *Handler) tooLarge(c *gin.Context) {
	_ = httpx.Fail(c, 413, "file_too_large", fmt.Sprintf("File is larger than the %d MB limit", h.maxBytes>>20))
}

func (h *Handler) absoluteURL(c *gin.Context, path string) string {
	if h.baseURL != "" {
		return h.baseURL + path
	}
	scheme := "http"
	if proto := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]); proto == "https" || proto == "http" {
		scheme = proto
	} else if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host + path
}
