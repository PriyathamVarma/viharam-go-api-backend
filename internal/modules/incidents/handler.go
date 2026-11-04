package incidents

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
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Handler struct{ db *mongodb.Database }

func NewHandler(db *mongodb.Database) *Handler { return &Handler{db: db} }
func admin(c *gin.Context) bool {
	claims := middleware.ClaimsFrom(c)
	return claims != nil && (claims.Role == "Admin" || claims.Role == "SuperAdmin")
}
func value(v any) string                { s, _ := v.(string); return strings.TrimSpace(s) }
func id(v string) (bson.ObjectID, bool) { x, err := bson.ObjectIDFromHex(v); return x, err == nil }

func (h *Handler) List(c *gin.Context) {
	if !admin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	filter := bson.M{}
	for _, key := range []string{"status", "severity", "reporterId"} {
		if v := c.Query(key); v != "" {
			filter[key] = v
		}
	}
	cur, err := h.db.Collection("incidents").Find(c, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch incidents")
		return
	}
	defer cur.Close(c)
	items := make([]bson.M, 0)
	if err := cur.All(c, &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch incidents")
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
	for _, key := range []string{"reporterName", "type", "severity", "description", "occurredAt"} {
		if value(body[key]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "reporterName, type, severity, description, and occurredAt are required")
			return
		}
	}
	occurred, err := time.Parse(time.RFC3339, value(body["occurredAt"]))
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "occurredAt must be an ISO timestamp")
		return
	}
	doc := bson.M{"reporterId": claims.UserID, "reporterName": value(body["reporterName"]), "bookingId": body["bookingId"], "experienceId": body["experienceId"], "type": value(body["type"]), "severity": value(body["severity"]), "description": value(body["description"]), "location": body["location"], "occurredAt": occurred, "photos": body["photos"], "status": "open", "createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC()}
	result, err := h.db.Collection("incidents").InsertOne(c, doc)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to report incident")
		return
	}
	doc["_id"] = result.InsertedID
	_ = httpx.Created(c, doc, "Incident reported")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	incidentID, ok := id(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var incident bson.M
	err := h.db.Collection("incidents").FindOne(c, bson.M{"_id": incidentID}).Decode(&incident)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Incident not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch incident")
		return
	}
	if !admin(c) && value(incident["reporterId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, incident, "")
}

func (h *Handler) Update(c *gin.Context) {
	if !admin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	incidentID, ok := id(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	set := bson.M{"updatedAt": time.Now().UTC()}
	for _, key := range []string{"status", "assignedTo", "resolution"} {
		if body[key] != nil {
			set[key] = body[key]
		}
	}
	if value(body["resolvedAt"]) != "" {
		parsed, err := time.Parse(time.RFC3339, value(body["resolvedAt"]))
		if err != nil {
			_ = httpx.Fail(c, 400, "invalid_request", "resolvedAt must be an ISO timestamp")
			return
		}
		set["resolvedAt"] = parsed
	}
	var updated bson.M
	err := h.db.Collection("incidents").FindOneAndUpdate(c, bson.M{"_id": incidentID}, bson.M{"$set": set}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Incident not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update incident")
		return
	}
	_ = httpx.OK(c, updated, "Incident updated")
}
