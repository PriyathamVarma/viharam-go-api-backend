package auth

import (
	"errors"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	user, token, err := h.service.Register(c.Request.Context(), input)
	if err != nil {
		if errors.Is(err, ErrEmailExists) {
			_ = httpx.Fail(c, 409, "email_exists", err.Error())
			return
		}
		if errors.Is(err, ErrPhoneExists) {
			_ = httpx.Fail(c, 409, "phone_exists", "That phone number is already registered")
			return
		}
		_ = httpx.Fail(c, 400, "invalid_request", err.Error())
		return
	}
	setToken(c, token)
	_ = httpx.Created(c, gin.H{"user": user, "token": token}, "Account created")
}

func (h *Handler) UpdateMe(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	user, err := h.service.UpdatePhone(c.Request.Context(), claims.UserID, body.Phone)
	if errors.Is(err, ErrPhoneExists) {
		_ = httpx.Fail(c, 409, "phone_exists", "That phone number is already registered")
		return
	}
	if errors.Is(err, ErrNotFound) {
		_ = httpx.Fail(c, 404, "user_not_found", "User not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", err.Error())
		return
	}
	_ = httpx.OK(c, user, "Phone number saved")
}
func (h *Handler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	user, token, err := h.service.Login(c.Request.Context(), input)
	if err != nil {
		_ = httpx.Fail(c, 401, "invalid_credentials", "Invalid email or password")
		return
	}
	setToken(c, token)
	_ = httpx.OK(c, gin.H{"user": user, "token": token}, "Signed in")
}

func (h *Handler) SupabaseLogin(c *gin.Context) {
	var input struct {
		AccessToken string `json:"accessToken"`
		Name        string `json:"name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.AccessToken == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "A Supabase access token is required")
		return
	}
	user, token, err := h.service.LoginWithSupabase(c.Request.Context(), input.AccessToken, input.Name)
	if err != nil {
		_ = httpx.Fail(c, 401, "invalid_credentials", "Apple sign-in could not be verified")
		return
	}
	setToken(c, token)
	_ = httpx.OK(c, gin.H{"user": user, "token": token}, "Signed in with Apple")
}
func (h *Handler) Me(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	user, err := h.service.repo.FindByID(c.Request.Context(), claims.UserID)
	if err != nil {
		_ = httpx.Fail(c, 404, "user_not_found", "User not found")
		return
	}
	_ = httpx.OK(c, user, "Current user")
}
func (h *Handler) Logout(c *gin.Context) {
	c.SetCookie("token", "", -1, "/", "", c.Request.URL.Scheme == "https", true)
	_ = httpx.OK(c, nil, "Signed out")
}

func (h *Handler) DeleteAccount(c *gin.Context) {
	h.RequestDeletion(c)
}

func (h *Handler) RequestDeletion(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	deletions, ok := h.service.repo.(AccountDeletionRepository)
	if !ok {
		_ = httpx.Fail(c, 501, "unsupported", "Account deletion review is only available with PostgreSQL")
		return
	}
	request, err := deletions.RequestDeletion(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, ErrDeletionPending) {
			_ = httpx.Fail(c, 409, "deletion_pending", "An account deletion request is already pending")
			return
		}
		if errors.Is(err, ErrNotFound) {
			_ = httpx.Fail(c, 404, "user_not_found", "User not found")
			return
		}
		_ = httpx.Fail(c, 500, "internal_error", "Could not request account deletion")
		return
	}
	c.SetCookie("token", "", -1, "/", "", c.Request.URL.Scheme == "https", true)
	_ = httpx.OK(c, gin.H{"request": request}, "Account deletion requested; an administrator can review it after seven days")
}

func (h *Handler) ListDeletionRequests(c *gin.Context) {
	deletions, ok := h.service.repo.(AccountDeletionRepository)
	if !ok {
		_ = httpx.Fail(c, 501, "unsupported", "Account deletion review is only available with PostgreSQL")
		return
	}
	items, err := deletions.ListDeletionRequests(c.Request.Context())
	if err != nil {
		_ = httpx.Fail(c, 500, "internal_error", "Could not load deletion requests")
		return
	}
	_ = httpx.OK(c, gin.H{"requests": items}, "Account deletion requests")
}

func (h *Handler) ApproveDeletion(c *gin.Context) {
	deletions, ok := h.service.repo.(AccountDeletionRepository)
	if !ok {
		_ = httpx.Fail(c, 501, "unsupported", "Account deletion review is only available with PostgreSQL")
		return
	}
	claims := middleware.ClaimsFrom(c)
	request, remote, err := deletions.ApproveDeletion(c.Request.Context(), c.Param("id"), claims.UserID)
	if err != nil {
		if errors.Is(err, ErrDeletionNotReady) {
			_ = httpx.Fail(c, 409, "deletion_not_ready", "The seven-day review period has not elapsed")
			return
		}
		if errors.Is(err, ErrDeletionNotFound) {
			_ = httpx.Fail(c, 404, "not_found", "Deletion request not found")
			return
		}
		_ = httpx.Fail(c, 500, "internal_error", "Could not complete account deletion")
		return
	}
	remoteDeleted := false
	if provider, subject, found := strings.Cut(remote, ":"); found && provider == "supabase" && subject != "" {
		if deleteErr := h.service.DeleteSupabaseUser(c.Request.Context(), subject); deleteErr == nil {
			remoteDeleted = true
		}
	}
	_ = httpx.OK(c, gin.H{"request": request, "remoteAuthDeleted": remoteDeleted}, "Account data deleted")
}

func (h *Handler) RejectDeletion(c *gin.Context) {
	deletions, ok := h.service.repo.(AccountDeletionRepository)
	if !ok {
		_ = httpx.Fail(c, 501, "unsupported", "Account deletion review is only available with PostgreSQL")
		return
	}
	claims := middleware.ClaimsFrom(c)
	request, err := deletions.RejectDeletion(c.Request.Context(), c.Param("id"), claims.UserID)
	if err != nil {
		if errors.Is(err, ErrDeletionNotFound) {
			_ = httpx.Fail(c, 404, "not_found", "Deletion request not found")
			return
		}
		_ = httpx.Fail(c, 500, "internal_error", "Could not reject account deletion")
		return
	}
	_ = httpx.OK(c, gin.H{"request": request}, "Account deletion rejected")
}

func (h *Handler) DemoLogin(c *gin.Context) {
	// Demo accounts remain disabled in production unless explicitly enabled for
	// a review/preview environment.
	if !demoAccountsEnabled() {
		_ = httpx.Fail(c, 404, "not_found", "Not found")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	accounts := map[string]RegisterInput{
		"Traveller":  {Name: "Demo Traveller", Email: "demo.traveller@viharam.dev", Password: "Demo@Viharam2024", Type: "Traveller"},
		"Host":       {Name: "Demo Host", Email: "demo.host@viharam.dev", Password: "Demo@Viharam2024", Type: "Host"},
		"Ambassador": {Name: "Demo Ambassador", Email: "demo.ambassador@viharam.dev", Password: "Demo@Viharam2024", Type: "Ambassador"},
		"Admin":      {Name: "Demo Admin", Email: "demo.admin@viharam.dev", Password: "Demo@Viharam2024", Type: "Admin"},
	}
	input, ok := accounts[body.Role]
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid demo role")
		return
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		_ = httpx.Fail(c, 500, "internal_error", "Failed to create demo account")
		return
	}
	user, err := h.service.repo.EnsureDemo(c.Request.Context(), input, passwordHash)
	if err != nil {
		_ = httpx.Fail(c, 500, "internal_error", "Failed to create demo account")
		return
	}
	token, err := h.service.token(user)
	if err != nil {
		_ = httpx.Fail(c, 500, "internal_error", "Failed to create session")
		return
	}
	setToken(c, token)
	_ = httpx.OK(c, gin.H{"id": user.ID, "name": user.Name, "email": user.Email, "type": user.Role, "token": token}, "Logged in as "+user.Name)
}

func demoAccountsEnabled() bool {
	if os.Getenv("ENABLE_DEMO_ACCOUNTS") == "true" {
		return true
	}
	return os.Getenv("APP_ENV") != "production" && os.Getenv("NODE_ENV") != "production"
}

func setToken(c *gin.Context, token string) {
	c.SetCookie("token", token, 7*24*60*60, "/", "", c.Request.URL.Scheme == "https", true)
}
