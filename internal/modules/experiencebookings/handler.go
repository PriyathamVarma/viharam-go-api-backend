package experiencebookings

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Handler struct{ db *mongodb.Database }

func NewHandler(db *mongodb.Database) *Handler { return &Handler{db: db} }

func (h *Handler) Slots(c *gin.Context) {
	experienceID := strings.TrimSpace(c.Query("experienceId"))
	month := strings.TrimSpace(c.Query("month"))
	if experienceID == "" || month == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceId and month are required")
		return
	}
	if len(month) != 7 || month[4] != '-' {
		_ = httpx.Fail(c, 400, "invalid_request", "month must be in YYYY-MM format")
		return
	}
	year, err := strconv.Atoi(month[:4])
	monthNumber, monthErr := strconv.Atoi(month[5:])
	if err != nil || monthErr != nil || monthNumber < 1 || monthNumber > 12 {
		_ = httpx.Fail(c, 400, "invalid_request", "month must be in YYYY-MM format")
		return
	}
	var experience bson.M
	err = h.db.Collection("experiences").FindOne(c.Request.Context(), bson.M{"$or": bson.A{bson.M{"id": experienceID}, bson.M{"slug": experienceID}}}).Decode(&experience)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Experience not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch experience")
		return
	}
	start, _ := time.Parse("2006-01-02", fmt.Sprintf("%04d-%02d-01", year, monthNumber))
	end := start.AddDate(0, 1, -1)
	cursor, err := h.db.Collection("experienceslots").Find(c.Request.Context(), bson.M{"experienceId": experienceID, "date": bson.M{"$gte": start.Format("2006-01-02"), "$lte": end.Format("2006-01-02")}}, mongooptions.Find().SetSort(bson.D{{Key: "date", Value: 1}}))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch slots")
		return
	}
	defer cursor.Close(c.Request.Context())
	items := make([]bson.M, 0)
	if err := cursor.All(c.Request.Context(), &items); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch slots")
		return
	}
	_ = httpx.OK(c, gin.H{"slots": items}, "")
}

func (h *Handler) SaveSlot(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !role(claims.Role, "Host", "Admin", "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Insufficient permissions")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, key := range []string{"experienceId", "date", "startTime", "endTime"} {
		if strings.TrimSpace(stringValue(body[key])) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "experienceId, date, startTime, endTime and capacity are required")
			return
		}
	}
	capacity := number(body["capacity"])
	if capacity < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceId, date, startTime, endTime and capacity are required")
		return
	}
	filter := bson.M{"experienceId": body["experienceId"], "date": body["date"]}
	set := bson.M{"startTime": body["startTime"], "endTime": body["endTime"], "capacity": capacity}
	if value, exists := body["priceOverride"]; exists {
		set["priceOverride"] = value
	}
	result := h.db.Collection("experienceslots").FindOneAndUpdate(c.Request.Context(), filter, bson.M{"$set": set, "$setOnInsert": bson.M{"bookedCount": 0, "lockedCount": 0, "isBlocked": false, "createdAt": time.Now().UTC()}}, mongooptions.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(mongooptions.After))
	var slot bson.M
	if err := result.Decode(&slot); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to save slot")
		return
	}
	_ = httpx.Created(c, slot, "Slot saved")
}

func (h *Handler) UpdateSlot(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !role(claims.Role, "Host", "Admin", "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Insufficient permissions")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid slot ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	set := bson.M{}
	for _, key := range []string{"capacity", "isBlocked", "startTime", "endTime", "priceOverride"} {
		if value, exists := body[key]; exists {
			set[key] = value
		}
	}
	result := h.db.Collection("experienceslots").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, bson.M{"$set": set}, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After))
	var slot bson.M
	if err := result.Decode(&slot); errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Slot not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update slot")
		return
	}
	if blocked, ok := body["isBlocked"].(bool); ok && blocked {
		h.cancelPendingForBlockedSlot(c, id)
	}
	_ = httpx.OK(c, slot, "Slot updated")
}

func (h *Handler) DeleteSlot(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !role(claims.Role, "Admin", "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Insufficient permissions")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid slot ID")
		return
	}
	var slot bson.M
	err := h.db.Collection("experienceslots").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&slot)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Slot not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch slot")
		return
	}
	if number(slot["bookedCount"]) > 0 {
		_ = httpx.Fail(c, 400, "has_bookings", "Cannot delete a slot with existing bookings")
		return
	}
	if _, err := h.db.Collection("experienceslots").DeleteOne(c.Request.Context(), bson.M{"_id": id}); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to delete slot")
		return
	}
	_ = httpx.OK(c, nil, "Slot deleted")
}

func (h *Handler) CreateLock(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	experienceID, guests := stringValue(body["experienceId"]), numberValue(body["guests"])
	if experienceID == "" || guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceId and guests (≥1) are required")
		return
	}
	slotID := stringValue(body["slotId"])
	if slotID == "" {
		date, startTime, endTime := stringValue(body["date"]), stringValue(body["startTime"]), stringValue(body["endTime"])
		if date == "" || startTime == "" || endTime == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "Provide slotId or date/startTime/endTime for a virtual slot")
			return
		}
		capacity := numberValue(body["capacity"])
		if capacity < 1 {
			capacity = 8
		}
		result := h.db.Collection("experienceslots").FindOneAndUpdate(c.Request.Context(), bson.M{"experienceId": experienceID, "date": date}, bson.M{"$setOnInsert": bson.M{"experienceId": experienceID, "date": date, "startTime": startTime, "endTime": endTime, "capacity": capacity, "bookedCount": 0, "lockedCount": 0, "isBlocked": false, "createdAt": time.Now().UTC()}}, mongooptions.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(mongooptions.After))
		var slot bson.M
		if err := result.Decode(&slot); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to create virtual slot")
			return
		}
		slotID = objectIDString(slot["_id"])
	}
	slotObjectID, ok := objectID(slotID)
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid slot ID")
		return
	}
	// Atomic capacity reservation prevents two concurrent requests from overselling a slot.
	availableExpression := bson.M{"$subtract": bson.A{
		bson.M{"$subtract": bson.A{
			bson.M{"$ifNull": bson.A{"$capacity", 0}},
			bson.M{"$ifNull": bson.A{"$bookedCount", 0}},
		}},
		bson.M{"$ifNull": bson.A{"$lockedCount", 0}},
	}}
	filter := bson.M{"_id": slotObjectID, "isBlocked": bson.M{"$ne": true}, "$expr": bson.M{"$gte": bson.A{availableExpression, guests}}}
	result := h.db.Collection("experienceslots").FindOneAndUpdate(c.Request.Context(), filter, bson.M{"$inc": bson.M{"lockedCount": guests}}, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After))
	var slot bson.M
	if err := result.Decode(&slot); errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 400, "unavailable", "Only limited spots are available for this slot")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to lock slot")
		return
	}
	lockID := uuid.NewString()
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	lock := bson.M{"lockId": lockID, "experienceId": experienceID, "slotId": slotID, "guests": guests, "sessionId": body["sessionId"], "expiresAt": expiresAt, "createdAt": time.Now().UTC()}
	if _, err := h.db.Collection("bookinglocks").InsertOne(c.Request.Context(), lock); err != nil {
		_, _ = h.db.Collection("experienceslots").UpdateOne(c.Request.Context(), bson.M{"_id": slotObjectID}, bson.M{"$inc": bson.M{"lockedCount": -guests}})
		_ = httpx.Fail(c, 500, "database_error", "Failed to create lock")
		return
	}
	_ = httpx.Created(c, gin.H{"lockId": lockID, "expiresAt": expiresAt, "slotId": slotID}, "Slot locked")
}

func (h *Handler) ReleaseLock(c *gin.Context) {
	lockID := strings.TrimSpace(c.Param("lockId"))
	if lockID == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "lockId is required")
		return
	}
	var lock bson.M
	err := h.db.Collection("bookinglocks").FindOne(c.Request.Context(), bson.M{"lockId": lockID}).Decode(&lock)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Lock not found or already expired")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch lock")
		return
	}
	if slotID, ok := objectID(stringValue(lock["slotId"])); ok {
		_, _ = h.db.Collection("experienceslots").UpdateOne(c.Request.Context(), bson.M{"_id": slotID}, bson.M{"$inc": bson.M{"lockedCount": -number(lock["guests"])}})
	}
	if _, err := h.db.Collection("bookinglocks").DeleteOne(c.Request.Context(), bson.M{"lockId": lockID}); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to release lock")
		return
	}
	_ = httpx.OK(c, nil, "Lock released")
}

func (h *Handler) List(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	filter := bson.M{}
	switch claims.Role {
	case "Traveller":
		filter["travelerId"] = claims.UserID
	case "Ambassador":
		filter["ambassadorId"] = claims.UserID
	case "Host":
		_ = httpx.OK(c, gin.H{"bookings": []bson.M{}, "total": 0, "page": 1, "limit": 20}, "")
		return
	}
	if status := c.Query("status"); status != "" {
		filter["status"] = status
	}
	if experienceID := c.Query("experienceId"); experienceID != "" {
		filter["experienceId"] = experienceID
	}
	page, limit := positive(c.Query("page"), 1), positive(c.Query("limit"), 20)
	if limit > 100 {
		limit = 100
	}
	collection := h.db.Collection("experiencebookings")
	cursor, err := collection.Find(c.Request.Context(), filter, mongooptions.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64((page-1)*limit)).SetLimit(int64(limit)))
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
	total, err := collection.CountDocuments(c.Request.Context(), filter)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to count bookings")
		return
	}
	_ = httpx.OK(c, gin.H{"bookings": items, "total": total, "page": page, "limit": limit}, "")
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
	for _, key := range []string{"lockId", "experienceId", "slotId", "travelerName", "travelerEmail", "travelerPhone", "emergencyContact", "date", "startTime"} {
		if stringValue(body[key]) == "" {
			_ = httpx.Fail(c, 400, "invalid_request", "Missing required booking fields")
			return
		}
	}
	guests := numberValue(body["guests"])
	if guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "Missing required booking fields")
		return
	}
	lockID := stringValue(body["lockId"])
	var lock bson.M
	err := h.db.Collection("bookinglocks").FindOne(c.Request.Context(), bson.M{"lockId": lockID}).Decode(&lock)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 400, "invalid_lock", "Lock not found or expired")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to validate lock")
		return
	}
	if stringValue(lock["slotId"]) != stringValue(body["slotId"]) || number(lock["guests"]) != guests {
		_ = httpx.Fail(c, 400, "invalid_lock", "Lock does not match booking")
		return
	}
	if expires, ok := lock["expiresAt"].(time.Time); ok && expires.Before(time.Now()) {
		_ = httpx.Fail(c, 400, "expired_lock", "Lock has expired")
		return
	}
	slotID, ok := objectID(stringValue(body["slotId"]))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid slot ID")
		return
	}
	var slot bson.M
	err = h.db.Collection("experienceslots").FindOne(c.Request.Context(), bson.M{"_id": slotID}).Decode(&slot)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Slot not found")
		return
	} else if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch slot")
		return
	}
	if slot["isBlocked"] == true || number(slot["capacity"])-number(slot["bookedCount"]) < guests {
		_ = httpx.Fail(c, 400, "unavailable", "This slot is not available")
		return
	}
	bookingRef := "VH-EXP-" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + strings.ToUpper(uuid.NewString()[:6])
	now := time.Now().UTC()
	doc := bson.M{"bookingRef": bookingRef, "experienceId": body["experienceId"], "slotId": body["slotId"], "travelerId": claims.UserID, "travelerName": body["travelerName"], "travelerEmail": body["travelerEmail"], "travelerPhone": body["travelerPhone"], "emergencyContact": body["emergencyContact"], "specialRequests": body["specialRequests"], "date": body["date"], "startTime": body["startTime"], "guests": guests, "pricePerPerson": body["pricePerPerson"], "subtotal": body["subtotal"], "serviceFee": body["serviceFee"], "total": body["total"], "status": "confirmed", "paymentStatus": "paid", "statusHistory": bson.A{bson.M{"status": "confirmed", "timestamp": now, "note": "Payment received", "changedBy": "system"}}, "createdAt": now, "updatedAt": now}
	result, err := h.db.Collection("experiencebookings").InsertOne(c.Request.Context(), doc)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create booking")
		return
	}
	doc["_id"] = result.InsertedID
	if _, err := h.db.Collection("experienceslots").UpdateOne(c.Request.Context(), bson.M{"_id": slotID, "lockedCount": bson.M{"$gte": guests}}, bson.M{"$inc": bson.M{"bookedCount": guests, "lockedCount": -guests}}); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update slot")
		return
	}
	_, _ = h.db.Collection("bookinglocks").DeleteOne(c.Request.Context(), bson.M{"lockId": lockID})
	_ = httpx.Created(c, doc, "Booking confirmed")
}

func (h *Handler) Get(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	if claims.Role != "Admin" && claims.Role != "SuperAdmin" && stringValue(booking["travelerId"]) != claims.UserID && !(claims.Role == "Ambassador" && stringValue(booking["ambassadorId"]) == claims.UserID) {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	_ = httpx.OK(c, booking, "")
}

func (h *Handler) Cancel(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	admin := role(claims.Role, "Admin", "SuperAdmin")
	if !admin && stringValue(booking["travelerId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	status := stringValue(booking["status"])
	if !admin && status != "confirmed" && status != "ambassador_assigned" {
		_ = httpx.Fail(c, 400, "invalid_status", fmt.Sprintf("Cannot cancel a booking with status \"%s\"", status))
		return
	}
	if status == "cancelled" || status == "refunded" || status == "completed" {
		_ = httpx.Fail(c, 400, "invalid_status", fmt.Sprintf("Booking is already %s", status))
		return
	}
	var body map[string]any
	_ = c.ShouldBindJSON(&body)
	now := time.Now().UTC()
	next := "cancelled"
	if stringValue(booking["paymentStatus"]) == "paid" {
		next = "refund_requested"
	}
	history := bson.M{"status": "cancelled", "timestamp": now, "note": stringValue(body["reason"]), "changedBy": claims.UserID}
	set := bson.M{"status": next, "cancellationReason": body["reason"], "cancelledAt": now, "cancelledBy": claims.UserID, "updatedAt": now}
	result := h.db.Collection("experiencebookings").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, bson.M{"$set": set, "$push": bson.M{"statusHistory": history}}, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After))
	var updated bson.M
	if err := result.Decode(&updated); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to cancel booking")
		return
	}
	if slotID, ok := objectID(stringValue(booking["slotId"])); ok {
		_, _ = h.db.Collection("experienceslots").UpdateOne(c.Request.Context(), bson.M{"_id": slotID}, bson.M{"$inc": bson.M{"bookedCount": -number(booking["guests"])}})
	}
	_ = httpx.OK(c, updated, "Booking cancelled")
}

func (h *Handler) Status(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !role(claims.Role, "Ambassador", "Admin", "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	target := stringValue(body["status"])
	allowed := map[string][]string{"ambassador_assigned": {"confirmed"}, "guest_checked_in": {"ambassador_assigned"}, "in_progress": {"guest_checked_in"}, "completed": {"in_progress"}, "disputed": {"pending_payment", "confirmed", "ambassador_assigned", "guest_checked_in", "in_progress", "completed", "cancelled", "refund_requested", "refunded"}}
	previous, exists := allowed[target]
	if !exists {
		_ = httpx.Fail(c, 400, "invalid_status", "Unknown or protected status")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	if claims.Role == "Ambassador" && stringValue(booking["ambassadorId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	current := stringValue(booking["status"])
	valid := false
	for _, value := range previous {
		if value == current {
			valid = true
		}
	}
	if !valid {
		_ = httpx.Fail(c, 400, "invalid_transition", fmt.Sprintf("Cannot transition from \"%s\" to \"%s\"", current, target))
		return
	}
	now := time.Now().UTC()
	set := bson.M{"status": target, "updatedAt": now}
	if target == "ambassador_assigned" && stringValue(body["ambassadorId"]) != "" {
		set["ambassadorId"] = body["ambassadorId"]
		set["assignedAt"] = now
	}
	if target == "guest_checked_in" {
		set["checkedInAt"] = now
	}
	if target == "completed" {
		set["completedAt"] = now
	}
	result := h.db.Collection("experiencebookings").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, bson.M{"$set": set, "$push": bson.M{"statusHistory": bson.M{"status": target, "timestamp": now, "note": body["note"], "changedBy": claims.UserID}}}, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After))
	var updated bson.M
	if err := result.Decode(&updated); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update status")
		return
	}
	_ = httpx.OK(c, updated, fmt.Sprintf("Status updated to \"%s\"", target))
}

func (h *Handler) Refund(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || !role(claims.Role, "Admin", "SuperAdmin") {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	amount := numberValue(body["refundAmount"])
	reason := stringValue(body["refundReason"])
	if amount < 0 || reason == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "refundAmount and refundReason are required")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	if !role(stringValue(booking["status"]), "refund_requested", "cancelled") {
		_ = httpx.Fail(c, 400, "invalid_status", fmt.Sprintf("Cannot refund a booking with status \"%s\"", stringValue(booking["status"])))
		return
	}
	now := time.Now().UTC()
	update := bson.M{"$set": bson.M{"status": "refunded", "paymentStatus": "refunded", "refundAmount": amount, "refundReason": reason, "refundedAt": now, "updatedAt": now}, "$push": bson.M{"statusHistory": bson.M{"status": "refunded", "timestamp": now, "note": reason, "changedBy": claims.UserID}}}
	var updated bson.M
	err = h.db.Collection("experiencebookings").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, update, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After)).Decode(&updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to process refund")
		return
	}
	_ = httpx.OK(c, updated, "Refund processed")
}

func (h *Handler) Reschedule(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := objectID(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid booking ID")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	newSlotID, newDate := stringValue(body["newSlotId"]), stringValue(body["newDate"])
	newSlot, valid := objectID(newSlotID)
	if !valid || newDate == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "newSlotId and newDate are required")
		return
	}
	var booking bson.M
	err := h.db.Collection("experiencebookings").FindOne(c.Request.Context(), bson.M{"_id": id}).Decode(&booking)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Booking not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch booking")
		return
	}
	admin := role(claims.Role, "Admin", "SuperAdmin")
	if !admin && stringValue(booking["travelerId"]) != claims.UserID {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if !admin && !role(stringValue(booking["status"]), "confirmed", "ambassador_assigned") {
		_ = httpx.Fail(c, 400, "invalid_status", "Booking cannot be rescheduled in its current status")
		return
	}
	var slot bson.M
	err = h.db.Collection("experienceslots").FindOne(c.Request.Context(), bson.M{"_id": newSlot}).Decode(&slot)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "New slot not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch new slot")
		return
	}
	guests := number(booking["guests"])
	available := number(slot["capacity"]) - number(slot["bookedCount"]) - number(slot["lockedCount"])
	if slot["isBlocked"] == true || available < guests {
		_ = httpx.Fail(c, 400, "unavailable", fmt.Sprintf("New slot only has %g spot(s) available", available))
		return
	}
	now := time.Now().UTC()
	set := bson.M{"slotId": newSlotID, "date": newDate, "originalSlotId": stringValue(booking["originalSlotId"]), "originalDate": stringValue(booking["originalDate"]), "rescheduledAt": now, "rescheduledBy": claims.UserID, "updatedAt": now}
	if set["originalSlotId"] == "" {
		set["originalSlotId"] = stringValue(booking["slotId"])
	}
	if set["originalDate"] == "" {
		set["originalDate"] = stringValue(booking["date"])
	}
	update := bson.M{"$set": set, "$push": bson.M{"statusHistory": bson.M{"status": booking["status"], "timestamp": now, "note": fmt.Sprintf("Rescheduled to %s: %s", newDate, stringValue(body["reason"])), "changedBy": claims.UserID}}}
	var updated bson.M
	err = h.db.Collection("experiencebookings").FindOneAndUpdate(c.Request.Context(), bson.M{"_id": id}, update, mongooptions.FindOneAndUpdate().SetReturnDocument(mongooptions.After)).Decode(&updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to reschedule booking")
		return
	}
	if oldSlot, ok := objectID(stringValue(booking["slotId"])); ok {
		_, _ = h.db.Collection("experienceslots").UpdateOne(c.Request.Context(), bson.M{"_id": oldSlot}, bson.M{"$inc": bson.M{"bookedCount": -guests}})
	}
	_, _ = h.db.Collection("experienceslots").UpdateOne(c.Request.Context(), bson.M{"_id": newSlot}, bson.M{"$inc": bson.M{"bookedCount": guests}})
	_ = httpx.OK(c, updated, "Booking rescheduled")
}

func (h *Handler) cancelPendingForBlockedSlot(c *gin.Context, id bson.ObjectID) {
	_, _ = h.db.Collection("experiencebookings").UpdateMany(c.Request.Context(), bson.M{"slotId": id.Hex(), "status": "pending_payment"}, bson.M{"$set": bson.M{"status": "cancelled", "cancellationReason": "Slot blocked by host/admin", "cancelledAt": time.Now().UTC(), "cancelledBy": "system"}})
}
func objectID(value string) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(value)
	return id, err == nil
}
func objectIDString(value any) string {
	if id, ok := value.(bson.ObjectID); ok {
		return id.Hex()
	}
	return stringValue(value)
}
func role(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if parsed, ok := value.(string); ok {
		return strings.TrimSpace(parsed)
	}
	return ""
}
func number(value any) float64 {
	switch parsed := value.(type) {
	case float64:
		return parsed
	case int:
		return float64(parsed)
	case int32:
		return float64(parsed)
	case int64:
		return float64(parsed)
	}
	return 0
}
func numberValue(value any) float64 { return number(value) }
func positive(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

var _ = math.Ceil
