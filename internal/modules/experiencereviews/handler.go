package experiencereviews

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

func (h *Handler) List(c *gin.Context) {
	filter := bson.M{"status": "approved"}
	claims := middleware.ClaimsFrom(c)
	if claims != nil && (claims.Role == "Admin" || claims.Role == "SuperAdmin") && c.Query("status") != "" {
		filter["status"] = c.Query("status")
	}
	for _, key := range []string{"subjectType", "subjectId", "travelerId"} {
		if value := strings.TrimSpace(c.Query(key)); value != "" {
			filter[key] = value
		}
	}
	cur, err := h.db.Collection("experiencereviews").Find(c, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch reviews")
		return
	}
	defer cur.Close(c)
	items := make([]bson.M, 0)
	if err := cur.All(c, &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch reviews")
		return
	}
	_ = httpx.OK(c, items, "")
}

func (h *Handler) Create(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.Role != "Traveller" {
		_ = httpx.Fail(c, 403, "forbidden", "Traveller access required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, key := range []string{"bookingId", "subjectType", "subjectId", "ratings", "title", "comment"} {
		if body[key] == nil || strings.TrimSpace(stringValue(body[key])) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "bookingId, subjectType, subjectId, ratings, title and comment are required")
			return
		}
	}
	subjectType := stringValue(body["subjectType"])
	if subjectType != "experience" && subjectType != "host" && subjectType != "ambassador" {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid subjectType")
		return
	}
	ratings, ok := body["ratings"].(map[string]any)
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_request", "ratings must be an object")
		return
	}
	for _, key := range []string{"overall", "guide", "communication", "value", "safety"} {
		value, valid := number(ratings[key])
		if !valid || value < 1 || value > 5 {
			_ = httpx.Fail(c, 400, "invalid_request", "Each rating must be between 1 and 5")
			return
		}
	}
	bookingID, ok := objectID(stringValue(body["bookingId"]))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c, bson.M{"_id": bookingID}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	if stringValue(booking["travelerId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "You can only review your own bookings")
		return
	}
	if stringValue(booking["status"]) != "completed" {
		_ = httpx.Fail(c, 400, "invalid_status", "You can only review completed bookings")
		return
	}
	filter := bson.M{"bookingId": stringValue(body["bookingId"]), "subjectType": subjectType}
	count, err := h.db.Collection("experiencereviews").CountDocuments(c, filter)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to validate review")
		return
	}
	if count > 0 {
		_ = httpx.Fail(c, 409, "duplicate", "You have already submitted a review for this booking and subject")
		return
	}
	doc := bson.M{"travelerId": claims.UserID, "travelerName": booking["travelerName"], "bookingId": stringValue(body["bookingId"]), "subjectType": subjectType, "subjectId": stringValue(body["subjectId"]), "ratings": ratings, "title": stringValue(body["title"]), "comment": stringValue(body["comment"]), "photos": body["photos"], "status": "approved", "reports": bson.A{}, "helpfulVotes": 0, "createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC()}
	result, err := h.db.Collection("experiencereviews").InsertOne(c, doc)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to submit review")
		return
	}
	doc["_id"] = result.InsertedID
	_ = httpx.Created(c, doc, "Review submitted")
}

func (h *Handler) Get(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	filter := bson.M{"_id": id}
	claims := middleware.ClaimsFrom(c)
	if claims == nil || (claims.Role != "Admin" && claims.Role != "SuperAdmin") {
		filter["status"] = "approved"
	}
	var review bson.M
	err := h.db.Collection("experiencereviews").FindOne(c, filter).Decode(&review)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch review")
		return
	}
	_ = httpx.OK(c, review, "")
}

func (h *Handler) Moderate(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || (claims.Role != "Admin" && claims.Role != "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	if status := stringValue(body["status"]); status != "" && !oneOf(status, "pending", "approved", "rejected", "flagged") {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid review status")
		return
	}
	set := bson.M{"moderatedBy": claims.Email, "moderatedAt": time.Now().UTC()}
	for _, key := range []string{"status", "moderationNote"} {
		if body[key] != nil {
			set[key] = body[key]
		}
	}
	var updated bson.M
	err := h.db.Collection("experiencereviews").FindOneAndUpdate(c, bson.M{"_id": id}, bson.M{"$set": set}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update review")
		return
	}
	_ = httpx.OK(c, updated, "Review updated")
}

func (h *Handler) Delete(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	result, err := h.db.Collection("experiencereviews").DeleteOne(c, bson.M{"_id": id})
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to delete review")
		return
	}
	if result.DeletedCount == 0 {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	_ = httpx.OK(c, nil, "Review deleted")
}

func (h *Handler) Helpful(c *gin.Context) {
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	var updated bson.M
	err := h.db.Collection("experiencereviews").FindOneAndUpdate(c, bson.M{"_id": id}, bson.M{"$inc": bson.M{"helpfulVotes": 1}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to mark review as helpful")
		return
	}
	_ = httpx.OK(c, updated, "Marked as helpful")
}

func (h *Handler) Report(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid review ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(stringValue(body["reason"])) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "reason is required")
		return
	}
	var review bson.M
	err := h.db.Collection("experiencereviews").FindOne(c, bson.M{"_id": id}).Decode(&review)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Review not found")
		return
	}
	if stringValue(review["travelerId"]) == claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "You cannot report your own review")
		return
	}
	reports, _ := review["reports"].(bson.A)
	nextStatus := any(nil)
	if len(reports)+1 >= 3 {
		nextStatus = "flagged"
	}
	set := bson.M{}
	if nextStatus != nil {
		set["status"] = nextStatus
	}
	update := bson.M{"$push": bson.M{"reports": bson.M{"reportedBy": claims.UserID, "reason": strings.TrimSpace(stringValue(body["reason"])), "reportedAt": time.Now().UTC()}}}
	if len(set) > 0 {
		update["$set"] = set
	}
	var updated bson.M
	err = h.db.Collection("experiencereviews").FindOneAndUpdate(c, bson.M{"_id": id}, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to report review")
		return
	}
	_ = httpx.Created(c, updated, "Review reported")
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func objectID(value string) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(value)
	return id, err == nil
}
