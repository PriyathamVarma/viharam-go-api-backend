package bookings

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Handler struct{ db *mongodb.Database }

func NewHandler(db *mongodb.Database) *Handler { return &Handler{db: db} }

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	filter := bson.M{}
	switch claims.Role {
	case "Admin", "SuperAdmin":
	case "Host":
		filter["hostId"] = claims.UserID
	case "Ambassador":
		filter["ambassadorId"] = claims.UserID
	default:
		filter["travelerId"] = claims.UserID
	}
	cursor, err := h.db.Collection("bookings").Find(c.Request.Context(), filter, mongooptions.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch bookings")
		return
	}
	defer cursor.Close(c.Request.Context())
	items := make([]bson.M, 0)
	if err := cursor.All(c.Request.Context(), &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch bookings")
		return
	}
	_ = httpx.OK(c, items, "")
}

func (h *Handler) Create(c *gin.Context) {
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
	propertyID, _ := body["propertyId"].(string)
	checkInRaw, _ := body["checkIn"].(string)
	checkOutRaw, _ := body["checkOut"].(string)
	guests, ok := number(body["guests"])
	if propertyID == "" || checkInRaw == "" || checkOutRaw == "" || !ok || guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "propertyId, checkIn, checkOut and guests are required")
		return
	}
	checkIn, err := time.Parse(time.RFC3339, checkInRaw)
	if err != nil {
		checkIn, err = time.Parse("2006-01-02", checkInRaw)
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid check-in date")
		return
	}
	checkOut, err := time.Parse(time.RFC3339, checkOutRaw)
	if err != nil {
		checkOut, err = time.Parse("2006-01-02", checkOutRaw)
	}
	if err != nil || !checkOut.After(checkIn) {
		_ = httpx.Fail(c, 400, "invalid_request", "Check-out must be after check-in")
		return
	}
	property, err := findProperty(c, h.db, propertyID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch property")
		return
	}
	if toString(property["status"]) != "active" {
		_ = httpx.Fail(c, 400, "unavailable", "Property is not available")
		return
	}
	price, ok := number(property["pricePerNight"])
	if !ok {
		_ = httpx.Fail(c, 500, "invalid_property", "Property price is invalid")
		return
	}
	nights := int(checkOut.Sub(checkIn).Hours() / 24)
	if nights < 1 {
		nights = 1
	}
	// Avoid double-booking overlapping active reservations for the property.
	overlap := bson.M{"propertyId": propertyID, "status": bson.M{"$in": bson.A{"pending", "confirmed"}}, "checkIn": bson.M{"$lt": checkOut}, "checkOut": bson.M{"$gt": checkIn}}
	count, err := h.db.Collection("bookings").CountDocuments(c.Request.Context(), overlap)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to check availability")
		return
	}
	if count > 0 {
		_ = httpx.Fail(c, 409, "unavailable", "Property is already booked for these dates")
		return
	}
	now := time.Now().UTC()
	doc := bson.M{"travelerId": claims.UserID, "propertyId": propertyID, "hostId": toString(property["hostId"]), "checkIn": checkIn, "checkOut": checkOut, "guests": guests, "totalPrice": float64(nights) * price, "pricePerNight": price, "nights": nights, "status": "pending", "createdAt": now, "updatedAt": now}
	if notes, ok := body["notes"]; ok {
		doc["notes"] = notes
	}
	result, err := h.db.Collection("bookings").InsertOne(c.Request.Context(), doc)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create booking")
		return
	}
	doc["_id"] = result.InsertedID
	_ = httpx.Created(c, doc, "Booking created")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	id, ok := objectID(c.Param("id"))
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var booking bson.M
	err := h.db.Collection("bookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && toString(booking["travelerId"]) != claims.UserID && toString(booking["hostId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, booking, "")
}

func (h *Handler) Update(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	id, ok := objectID(c.Param("id"))
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var booking bson.M
	err := h.db.Collection("bookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	status := toString(booking["status"])
	canUpdate := claims.Role == "Admin" || claims.Role == "SuperAdmin" || toString(booking["hostId"]) == claims.UserID || (toString(booking["travelerId"]) == claims.UserID && status == "pending")
	if !canUpdate {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, key := range []string{"_id", "travelerId", "propertyId", "hostId", "totalPrice", "pricePerNight", "nights", "createdAt"} {
		delete(body, key)
	}
	body["updatedAt"] = time.Now().UTC()
	result := h.db.Collection("bookings").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, bson.M{"$set": body}, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After))
	var updated bson.M
	if err := result.Decode(&updated); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update booking")
		return
	}
	_ = httpx.OK(c, updated, "Booking updated")
}

func findProperty(c *gin.Context, db *mongodb.Database, id string) (bson.M, error) {
	var property bson.M
	err := db.Collection("properties").FindOne(c.Request.Context(), bson.M{"_id": propertyID(id)}).Decode(&property)
	return property, err
}

func propertyID(id string) any {
	if objectID, ok := objectID(id); ok {
		return objectID
	}
	return id
}

func objectID(value string) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(value)
	return id, err == nil
}

func number(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(toStringValue(value))
}

func toStringValue(value any) string {
	if stringValue, ok := value.(string); ok {
		return stringValue
	}
	return ""
}
