package properties

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

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
	page := positiveInt(c.Query("page"), 1)
	limit := positiveInt(c.Query("limit"), 12)
	if limit > 100 {
		limit = 100
	}
	filter := bson.M{"status": "active"}
	if city := strings.TrimSpace(c.Query("city")); city != "" {
		filter["city"] = primitiveRegex(city)
	}
	if typ := strings.TrimSpace(c.Query("type")); typ != "" {
		filter["type"] = typ
	}

	ctx := c.Request.Context()
	collection := h.db.Collection("properties")
	findOpts := mongooptions.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64((page - 1) * limit)).SetLimit(int64(limit))
	cursor, err := collection.Find(ctx, filter, findOpts)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch properties")
		return
	}
	defer cursor.Close(ctx)
	items := make([]bson.M, 0)
	if err := cursor.All(ctx, &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch properties")
		return
	}
	total, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to count properties")
		return
	}
	_ = httpx.OK(c, gin.H{"items": items, "meta": gin.H{"total": total, "page": page, "limit": limit, "totalPages": int(math.Ceil(float64(total) / float64(limit)))}}, "")
}

func (h *Handler) Create(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || (claims.Role != "Host" && claims.Role != "Admin") {
		_ = httpx.Fail(c, 403, "forbidden", "Only hosts can create properties")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, field := range []string{"name", "description", "city", "country", "pricePerNight", "maxGuests"} {
		if value, ok := body[field]; !ok || value == nil || strings.TrimSpace(toString(value)) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "name, description, city, country, pricePerNight and maxGuests are required")
			return
		}
	}
	body["hostId"] = claims.UserID
	if _, ok := body["status"]; !ok {
		body["status"] = "draft"
	}
	if _, ok := body["type"]; !ok {
		body["type"] = "hotel"
	}
	result, err := h.db.Collection("properties").InsertOne(c.Request.Context(), body)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create property")
		return
	}
	body["_id"] = result.InsertedID
	_ = httpx.Created(c, body, "Property created")
}

func (h *Handler) Get(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var doc bson.M
	err := h.db.Collection("properties").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch property")
		return
	}
	_ = httpx.OK(c, doc, "")
}

func (h *Handler) Update(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	claims := middleware.ClaimsFrom(c)
	var existing bson.M
	err := h.db.Collection("properties").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch property")
		return
	}
	if claims == nil || (toString(existing["hostId"]) != claims.UserID && claims.Role != "Admin" && claims.Role != "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	delete(body, "_id")
	delete(body, "hostId")
	result := h.db.Collection("properties").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, bson.M{"$set": body}, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After))
	var updated bson.M
	if err := result.Decode(&updated); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to decode property")
		return
	}
	_ = httpx.OK(c, updated, "Property updated")
}

func (h *Handler) Delete(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	claims := middleware.ClaimsFrom(c)
	var existing bson.M
	err := h.db.Collection("properties").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch property")
		return
	}
	if claims == nil || (toString(existing["hostId"]) != claims.UserID && claims.Role != "Admin" && claims.Role != "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if _, err := h.db.Collection("properties").DeleteOne(c.Request.Context(), bson.M{"_id": id}); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to delete property")
		return
	}
	_ = httpx.OK(c, nil, "Property deleted")
}

func objectID(value string) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(value)
	return id, err == nil
}

func primitiveRegex(value string) bson.Regex {
	return bson.Regex{Pattern: regexp.QuoteMeta(value), Options: "i"}
}

func positiveInt(value string, fallback int) int {
	var result int
	if _, err := fmt.Sscanf(value, "%d", &result); err != nil || result < 1 {
		return fallback
	}
	return result
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
