package email

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Sender interface {
	SendBookingConfirmation(to string, payload map[string]any) error
}
type Handler struct {
	db     *mongodb.Database
	sender Sender
}

func NewHandler(db *mongodb.Database, sender Sender) *Handler {
	return &Handler{db: db, sender: sender}
}

func (h *Handler) BookingConfirmation(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	title := strings.TrimSpace(stringValue(body["experienceTitle"]))
	if title == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceTitle is required")
		return
	}
	bookingFilter := bson.M{}
	if reference := strings.TrimSpace(stringValue(body["bookingRef"])); reference != "" {
		bookingFilter["bookingRef"] = reference
	} else if id, err := bson.ObjectIDFromHex(stringValue(body["bookingId"])); err == nil {
		bookingFilter["_id"] = id
	} else {
		_ = httpx.Fail(c, 400, "invalid_request", "bookingRef or bookingId is required")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c, bookingFilter).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to validate booking")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && stringValue(booking["travelerId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	to := stringValue(booking["travelerEmail"])
	if to == "" {
		_ = httpx.Fail(c, 400, "invalid_booking", "Booking has no recipient email")
		return
	}
	body["travelerEmail"] = to
	if h.sender == nil {
		_ = httpx.Fail(c, 503, "email_unavailable", "Email provider is not configured")
		return
	}
	if err := h.sender.SendBookingConfirmation(to, body); err != nil {
		_ = httpx.Fail(c, 502, "email_delivery_failed", "Failed to send booking confirmation")
		return
	}
	_ = httpx.OK(c, gin.H{"queued": true, "to": to}, "Booking confirmation sent")
}

func stringValue(value any) string { parsed, _ := value.(string); return parsed }
