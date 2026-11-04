package support

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

type Handler struct{ db *mongodb.Database }

func NewHandler(db *mongodb.Database) *Handler { return &Handler{db: db} }
func admin(c *gin.Context) bool {
	claims := middleware.ClaimsFrom(c)
	return claims != nil && (claims.Role == "Admin" || claims.Role == "SuperAdmin")
}
func oid(value string) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(value)
	return id, err == nil
}
func text(value any) string { parsed, _ := value.(string); return strings.TrimSpace(parsed) }

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	filter := bson.M{}
	if !admin(c) {
		filter["userId"] = claims.UserID
	}
	for _, key := range []string{"status", "priority", "category"} {
		if value := c.Query(key); value != "" {
			filter[key] = value
		}
	}
	page, limit := pageLimit(c)
	cur, err := h.db.Collection("supporttickets").Find(c, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64((page-1)*limit)).SetLimit(int64(limit)))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch support tickets")
		return
	}
	defer cur.Close(c)
	items := make([]bson.M, 0)
	if err := cur.All(c, &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch support tickets")
		return
	}
	total, _ := h.db.Collection("supporttickets").CountDocuments(c, filter)
	_ = httpx.OK(c, gin.H{"tickets": items, "total": total, "page": page, "limit": limit}, "")
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
	category, subject, message := text(body["category"]), text(body["subject"]), text(body["message"])
	if category == "" || subject == "" || message == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "category, subject and message are required")
		return
	}
	now := time.Now().UTC()
	doc := bson.M{"userId": claims.UserID, "userName": claims.Email, "userEmail": claims.Email, "bookingId": body["bookingId"], "experienceId": body["experienceId"], "category": category, "subject": subject, "priority": body["priority"], "status": "open", "messages": bson.A{bson.M{"senderId": claims.UserID, "senderName": claims.Email, "senderRole": "user", "message": message, "attachments": bson.A{}, "createdAt": now}}, "adminNotes": bson.A{}, "createdAt": now, "updatedAt": now}
	if doc["priority"] == nil || text(doc["priority"]) == "" {
		doc["priority"] = "medium"
	}
	result, err := h.db.Collection("supporttickets").InsertOne(c, doc)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create support ticket")
		return
	}
	doc["_id"] = result.InsertedID
	_ = httpx.Created(c, doc, "Support ticket created")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := oid(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var ticket bson.M
	err := h.db.Collection("supporttickets").FindOne(c, bson.M{"_id": id}).Decode(&ticket)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch ticket")
		return
	}
	if !admin(c) && text(ticket["userId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if !admin(c) {
		delete(ticket, "adminNotes")
	}
	_ = httpx.OK(c, ticket, "")
}

func (h *Handler) Update(c *gin.Context) {
	if !admin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := oid(c.Param("id"))
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
	for _, key := range []string{"status", "priority", "assignedTo"} {
		if body[key] != nil {
			set[key] = body[key]
		}
	}
	if text(body["status"]) == "resolved" {
		set["resolvedAt"], set["resolvedBy"] = time.Now().UTC(), middleware.ClaimsFrom(c).Email
	}
	set["messages"] = bson.M{"senderId": "system", "senderName": "System", "senderRole": "system", "message": "Ticket status updated", "attachments": bson.A{}, "createdAt": time.Now().UTC()}
	var updated bson.M
	err := h.db.Collection("supporttickets").FindOneAndUpdate(c, bson.M{"_id": id}, bson.M{"$set": set, "$push": bson.M{"messages": set["messages"]}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update ticket")
		return
	}
	_ = httpx.OK(c, updated, "Ticket updated")
}

func (h *Handler) Message(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := oid(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var ticket bson.M
	if err := h.db.Collection("supporttickets").FindOne(c, bson.M{"_id": id}).Decode(&ticket); err != nil {
		_ = httpx.Fail(c, 404, "not_found", "Ticket not found")
		return
	}
	if !admin(c) && text(ticket["userId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["message"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "message is required")
		return
	}
	status := any(nil)
	if admin(c) {
		status = "waiting_user"
	} else if text(ticket["status"]) == "waiting_user" {
		status = "in_progress"
	}
	msg := bson.M{"senderId": claims.UserID, "senderName": claims.Email, "senderRole": map[bool]string{true: "admin", false: "user"}[admin(c)], "message": text(body["message"]), "attachments": body["attachments"], "createdAt": time.Now().UTC()}
	update := bson.M{"$push": bson.M{"messages": msg}}
	if status != nil {
		update["$set"] = bson.M{"status": status}
	}
	var updated bson.M
	err := h.db.Collection("supporttickets").FindOneAndUpdate(c, bson.M{"_id": id}, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to send message")
		return
	}
	_ = httpx.OK(c, updated, "Message sent")
}

func (h *Handler) AdminNote(c *gin.Context) {
	if !admin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := oid(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["note"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "note is required")
		return
	}
	claims := middleware.ClaimsFrom(c)
	note := bson.M{"adminEmail": claims.Email, "note": text(body["note"]), "createdAt": time.Now().UTC()}
	_, err := h.db.Collection("supporttickets").UpdateOne(c, bson.M{"_id": id}, bson.M{"$push": bson.M{"adminNotes": note}})
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to add admin note")
		return
	}
	_ = httpx.Created(c, note, "Admin note added")
}

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
