package uploads

import (
	"encoding/json"
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
	baseURL       string
	serviceKey    string
	publicBucket  string
	privateBucket string
	client        *http.Client
}

func (s *supabaseStorage) bucketFor(purpose string) (string, bool) {
	if purpose == "private_document" {
		return s.privateBucket, true
	}
	return s.publicBucket, false
}

func (s *supabaseStorage) put(c *gin.Context, bucket, name, contentType string, body io.Reader, size int64) error {
	endpoint := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, url.PathEscape(bucket), url.PathEscape(name))
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

func (s *supabaseStorage) publicURL(bucket, name string) string {
	return fmt.Sprintf("%s/storage/v1/object/public/%s/%s", s.baseURL, url.PathEscape(bucket), url.PathEscape(name))
}

func (s *supabaseStorage) signedURL(c *gin.Context, bucket, name string, expires int) (string, error) {
	endpoint := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", s.baseURL, url.PathEscape(bucket), url.PathEscape(name))
	body := strings.NewReader(fmt.Sprintf(`{"expiresIn":%d}`, expires))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.serviceKey)
	req.Header.Set("apikey", s.serviceKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return "", fmt.Errorf("supabase signed URL returned %s: %s", res.Status, strings.TrimSpace(string(message)))
	}
	var payload struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.SignedURL == "" {
		return "", fmt.Errorf("supabase returned an empty signed URL")
	}
	if strings.HasPrefix(payload.SignedURL, "http") {
		return payload.SignedURL, nil
	}
	return s.baseURL + "/storage/v1" + payload.SignedURL, nil
}
