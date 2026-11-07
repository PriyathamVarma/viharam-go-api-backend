package uploads

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// supabaseStorage uses the server-only Storage REST API. The service-role key
// never leaves the Go API and is never returned to the mobile/web clients.
type supabaseStorage struct {
	baseURL    string
	serviceKey string
	bucket     string
	client     *http.Client
}

func (s *supabaseStorage) put(c *gin.Context, name, contentType string, body io.Reader, size int64) error {
	endpoint := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, url.PathEscape(s.bucket), url.PathEscape(name))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.serviceKey)
	req.Header.Set("apikey", s.serviceKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "false")
	if size >= 0 {
		req.ContentLength = size
		req.Header.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("supabase storage returned %s: %s", res.Status, strings.TrimSpace(string(message)))
	}
	return nil
}

func (s *supabaseStorage) publicURL(name string) string {
	return fmt.Sprintf("%s/storage/v1/object/public/%s/%s", s.baseURL, url.PathEscape(s.bucket), url.PathEscape(name))
}
