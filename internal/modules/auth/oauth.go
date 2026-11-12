package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type supabaseUser struct {
	ID           string         `json:"id"`
	Email        string         `json:"email"`
	UserMetadata map[string]any `json:"user_metadata"`
}

func (s *Service) LoginWithSupabase(ctx context.Context, accessToken, fallbackName string) (User, string, error) {
	if strings.TrimSpace(s.supabaseURL) == "" || strings.TrimSpace(s.supabaseKey) == "" {
		return User{}, "", errors.New("Supabase authentication is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.supabaseURL, "/")+"/auth/v1/user", nil)
	if err != nil {
		return User{}, "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("apikey", s.supabaseKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return User{}, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var body struct {
			Msg              string `json:"msg"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		detail := strings.TrimSpace(body.Msg)
		if detail == "" {
			detail = strings.TrimSpace(body.ErrorDescription)
		}
		if detail == "" {
			detail = strings.TrimSpace(body.Error)
		}
		if detail != "" {
			return User{}, "", fmt.Errorf("Supabase token rejected (%d): %s", res.StatusCode, detail)
		}
		return User{}, "", fmt.Errorf("Supabase token rejected (%d)", res.StatusCode)
	}
	var remote supabaseUser
	if err := json.NewDecoder(res.Body).Decode(&remote); err != nil {
		return User{}, "", err
	}
	if remote.ID == "" || remote.Email == "" {
		return User{}, "", errors.New("Supabase did not return an email identity")
	}
	name := strings.TrimSpace(fallbackName)
	if name == "" {
		if v, ok := remote.UserMetadata["full_name"].(string); ok {
			name = v
		}
	}
	if name == "" {
		if v, ok := remote.UserMetadata["name"].(string); ok {
			name = v
		}
	}
	if name == "" {
		name = strings.Split(remote.Email, "@")[0]
	}
	user, err := s.repo.FindOrCreateOAuth(ctx, "supabase", remote.ID, name, remote.Email)
	if err != nil {
		return User{}, "", err
	}
	token, err := s.token(user)
	return user, token, err
}

func (s *Service) DeleteSupabaseUser(ctx context.Context, subject string) error {
	if strings.TrimSpace(s.supabaseURL) == "" || strings.TrimSpace(s.supabaseKey) == "" || strings.TrimSpace(subject) == "" {
		return errors.New("Supabase authentication is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(s.supabaseURL, "/")+"/auth/v1/admin/users/"+subject, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.supabaseKey)
	req.Header.Set("apikey", s.supabaseKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Supabase user deletion failed (%d)", res.StatusCode)
	}
	return nil
}
