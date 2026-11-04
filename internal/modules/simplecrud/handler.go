package simplecrud

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Handler struct {
	db         *mongodb.Database
	collection string
	listKey    string
	adminOnly  bool
}

func New(db *mongodb.Database, collection, listKey string, adminOnly bool) *Handler {
	return &Handler{db: db, collection: collection, listKey: listKey, adminOnly: adminOnly}
}

func (h *Handler) List(c *gin.Context) {
	if h.adminOnly && !isAdmin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	filter := bson.M{}
	if h.collection == "hostprofiles" && !isAdmin(c) {
		if middleware.ClaimsFrom(c).Role != "Host" {
			_ = httpx.Fail(c, 403, "forbidden", "Host access required")
			return
		}
		filter["userId"] = middleware.ClaimsFrom(c).UserID
	}
	if h.collection == "ambassadorassignments" && !isAdmin(c) {
		if middleware.ClaimsFrom(c).Role != "Ambassador" {
			_ = httpx.Fail(c, 403, "forbidden", "Ambassador access required")
			return
		}
		filter["ambassadorId"] = middleware.ClaimsFrom(c).UserID
	}
	if h.collection == "itineraries" && !isAdmin(c) {
		filter["userId"] = middleware.ClaimsFrom(c).UserID
	}
	for _, key := range []string{"status", "category", "city", "creatorType", "userId", "experienceId"} {
		if v := strings.TrimSpace(c.Query(key)); v != "" {
			filter[key] = v
		}
	}
	page, limit := pageLimit(c)
	cur, err := h.db.Collection(h.collection).Find(c, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64((page-1)*limit)).SetLimit(int64(limit)))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch records")
		return
	}
	defer cur.Close(c)
	items := make([]bson.M, 0)
	if err := cur.All(c, &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch records")
		return
	}
	total, _ := h.db.Collection(h.collection).CountDocuments(c, filter)
	_ = httpx.OK(c, gin.H{h.listKey: items, "total": total, "page": page, "limit": limit}, "")
}

func (h *Handler) Create(c *gin.Context) {
	if h.adminOnly && !isAdmin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if h.collection == "hostprofiles" && claims.Role != "Host" && !isAdmin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Host access required")
		return
	}
	if h.collection == "ambassadorassignments" && !isAdmin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	if h.collection == "creatorprofiles" {
		body["userId"] = claims.UserID
	}
	if h.collection == "itineraries" {
		body["userId"] = claims.UserID
	}
	body["createdAt"], body["updatedAt"] = time.Now().UTC(), time.Now().UTC()
	result, err := h.db.Collection(h.collection).InsertOne(c, body)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create record")
		return
	}
	body["_id"] = result.InsertedID
	_ = httpx.Created(c, body, "Record created")
}

func (h *Handler) Get(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var doc bson.M
	err := h.db.Collection(h.collection).FindOne(c, bson.M{"_id": id}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch record")
		return
	}
	if !h.canAccess(c, doc) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if h.collection == "itineraries" && !isAdmin(c) && stringValue(doc["userId"]) != middleware.ClaimsFrom(c).UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, doc, "")
}

func (h *Handler) Update(c *gin.Context) {
	if h.adminOnly && !isAdmin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var existing bson.M
	if err := h.db.Collection(h.collection).FindOne(c, bson.M{"_id": id}).Decode(&existing); errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch record")
		return
	}
	if !h.canAccess(c, existing) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	delete(body, "_id")
	delete(body, "createdAt")
	body["updatedAt"] = time.Now().UTC()
	var updated bson.M
	err := h.db.Collection(h.collection).FindOneAndUpdate(c, bson.M{"_id": id}, bson.M{"$set": body}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update record")
		return
	}
	_ = httpx.OK(c, updated, "Record updated")
}

func (h *Handler) Delete(c *gin.Context) {
	if h.adminOnly && !isAdmin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var existing bson.M
	if err := h.db.Collection(h.collection).FindOne(c, bson.M{"_id": id}).Decode(&existing); errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch record")
		return
	}
	if !h.canAccess(c, existing) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	result, err := h.db.Collection(h.collection).DeleteOne(c, bson.M{"_id": id})
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to delete record")
		return
	}
	if result.DeletedCount == 0 {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	_ = httpx.OK(c, nil, "Record deleted")
}

func (h *Handler) Availability(c *gin.Context) {
	if !isAdmin(c) && middleware.ClaimsFrom(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	filter := bson.M{"_id": id}
	var doc bson.M
	if err := h.db.Collection(h.collection).FindOne(c, filter).Decode(&doc); err != nil {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if c.Request.Method == "PATCH" {
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err != nil {
			_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
			return
		}
		var updated bson.M
		err := h.db.Collection(h.collection).FindOneAndUpdate(c, filter, bson.M{"$set": bson.M{"availability": body, "updatedAt": time.Now().UTC()}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
		if err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to update availability")
			return
		}
		_ = httpx.OK(c, updated, "Availability updated")
		return
	}
	_ = httpx.OK(c, doc["availability"], "")
}

func (h *Handler) Waiver(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || stringValue(body["bookingId"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "bookingId is required")
		return
	}
	var updated bson.M
	err := h.db.Collection(h.collection).FindOneAndUpdate(c, bson.M{"_id": id, "userId": claims.UserID}, bson.M{"$set": bson.M{"waiver": bson.M{"bookingId": body["bookingId"], "acceptedAt": time.Now().UTC()}, "updatedAt": time.Now().UTC()}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Safety profile not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to record waiver")
		return
	}
	_ = httpx.OK(c, updated, "Waiver recorded")
}

func (h *Handler) GetByToken(c *gin.Context) {
	if middleware.ClaimsFrom(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var doc bson.M
	err := h.db.Collection(h.collection).FindOne(c, bson.M{"token": c.Param("token")}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch record")
		return
	}
	_ = httpx.OK(c, doc, "")
}

func (h *Handler) canAccess(c *gin.Context, doc bson.M) bool {
	if isAdmin(c) {
		return true
	}
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		return false
	}
	switch h.collection {
	case "hostprofiles":
		return claims.Role == "Host" && stringValue(doc["userId"]) == claims.UserID
	case "ambassadorassignments":
		return claims.Role == "Ambassador" && stringValue(doc["ambassadorId"]) == claims.UserID
	default:
		return true
	}
}

func isAdmin(c *gin.Context) bool {
	claims := middleware.ClaimsFrom(c)
	return claims != nil && (claims.Role == "Admin" || claims.Role == "SuperAdmin")
}
func objectID(v string) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(v)
	return id, err == nil
}
func stringValue(v any) string { s, _ := v.(string); return s }
func pageLimit(c *gin.Context) (int, int) {
	page, limit := 1, 20
	if _, err := fmt.Sscan(c.Query("page"), &page); err != nil || page < 1 {
		page = 1
	}
	if _, err := fmt.Sscan(c.Query("limit"), &limit); err != nil || limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit
}
