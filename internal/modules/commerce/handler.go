package commerce

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

func New(db *mongodb.Database) *Handler        { return &Handler{db: db} }
func claims(c *gin.Context) *middleware.Claims { return middleware.ClaimsFrom(c) }
func admin(c *gin.Context) bool {
	x := claims(c)
	return x != nil && (x.Role == "Admin" || x.Role == "SuperAdmin")
}
func text(v any) string                  { s, _ := v.(string); return strings.TrimSpace(s) }
func oid(v string) (bson.ObjectID, bool) { id, err := bson.ObjectIDFromHex(v); return id, err == nil }

func (h *Handler) List(c *gin.Context, collection string, key string, adminOnly bool) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	if adminOnly && !admin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	filter := bson.M{}
	if !adminOnly {
		filter["userId"] = claims(c).UserID
	}
	if v := c.Query("status"); v != "" {
		filter["status"] = v
	}
	cur, err := h.db.Collection(collection).Find(c, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
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
	if key == "" {
		_ = httpx.OK(c, items, "")
		return
	}
	_ = httpx.OK(c, gin.H{key: items, "total": len(items)}, "")
}

func (h *Handler) Create(c *gin.Context, collection string, required []string, message string) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	for _, key := range required {
		if text(body[key]) == "" && body[key] == nil {
			_ = httpx.Fail(c, 400, "invalid_request", "Missing required fields")
			return
		}
	}
	body["userId"], body["createdAt"], body["updatedAt"] = claims(c).UserID, time.Now().UTC(), time.Now().UTC()
	result, err := h.db.Collection(collection).InsertOne(c, body)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create record")
		return
	}
	body["_id"] = result.InsertedID
	_ = httpx.Created(c, body, message)
}

func (h *Handler) Get(c *gin.Context, collection string) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	id, ok := oid(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	var doc bson.M
	err := h.db.Collection(collection).FindOne(c, bson.M{"_id": id}).Decode(&doc)
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

func (h *Handler) Update(c *gin.Context, collection, message string) {
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
	delete(body, "_id")
	delete(body, "createdAt")
	body["updatedAt"] = time.Now().UTC()
	var updated bson.M
	err := h.db.Collection(collection).FindOneAndUpdate(c, bson.M{"_id": id}, bson.M{"$set": body}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Record not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update record")
		return
	}
	_ = httpx.OK(c, updated, message)
}

func (h *Handler) Delete(c *gin.Context, collection string) {
	if !admin(c) {
		_ = httpx.Fail(c, 403, "forbidden", "Admin access required")
		return
	}
	id, ok := oid(c.Param("id"))
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid ID")
		return
	}
	result, err := h.db.Collection(collection).DeleteOne(c, bson.M{"_id": id})
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

func (h *Handler) ValidateCoupon(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["code"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "Missing required fields: code, bookingAmount")
		return
	}
	var coupon bson.M
	err := h.db.Collection("coupons").FindOne(c, bson.M{"code": strings.ToUpper(text(body["code"]))}).Decode(&coupon)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.OK(c, gin.H{"valid": false, "reason": "Coupon not found"}, "Validation result")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to validate coupon")
		return
	}
	_ = httpx.OK(c, gin.H{"valid": true, "coupon": coupon}, "Validation result")
}

func (h *Handler) ApplyReferral(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["referralCode"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "Missing required field: referralCode")
		return
	}
	var updated bson.M
	err := h.db.Collection("referrals").FindOneAndUpdate(c, bson.M{"code": text(body["referralCode"]), "status": "active"}, bson.M{"$set": bson.M{"refereeId": claims(c).UserID, "status": "applied", "updatedAt": time.Now().UTC()}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Invalid or inactive referral code")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to apply referral")
		return
	}
	_ = httpx.OK(c, updated, "Referral applied successfully")
}

func (h *Handler) Loyalty(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var loyalty bson.M
	err := h.db.Collection("loyalties").FindOne(c, bson.M{"userId": claims(c).UserID}).Decode(&loyalty)
	if errors.Is(err, mongo.ErrNoDocuments) {
		loyalty = bson.M{"userId": claims(c).UserID, "xp": 0, "level": 1, "createdAt": time.Now().UTC()}
		_, err = h.db.Collection("loyalties").InsertOne(c, loyalty)
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to load loyalty profile")
		return
	}
	_ = httpx.OK(c, gin.H{"loyalty": loyalty}, "Loyalty profile fetched successfully")
}

func (h *Handler) Earn(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["type"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "type is required")
		return
	}
	var loyalty bson.M
	err := h.db.Collection("loyalties").FindOneAndUpdate(c, bson.M{"userId": claims(c).UserID}, bson.M{"$inc": bson.M{"xp": 10}, "$set": bson.M{"updatedAt": time.Now().UTC()}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&loyalty)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to award loyalty points")
		return
	}
	_ = httpx.OK(c, gin.H{"loyalty": loyalty}, "XP awarded")
}

func (h *Handler) PricingRules(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	experienceID := text(c.Query("experienceId"))
	if experienceID == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceId is required")
		return
	}
	cur, err := h.db.Collection("pricingrules").Find(c, bson.M{"experienceId": experienceID, "isActive": true})
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch pricing rules")
		return
	}
	defer cur.Close(c)
	rules := make([]bson.M, 0)
	if err := cur.All(c, &rules); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch pricing rules")
		return
	}
	_ = httpx.OK(c, rules, "")
}

func (h *Handler) CalculatePricing(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	experienceID, date := text(body["experienceId"]), text(body["date"])
	adultGuests := number(body["adultGuests"])
	if experienceID == "" || date == "" || adultGuests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "experienceId, date and adultGuests are required")
		return
	}
	var experience bson.M
	err := h.db.Collection("experiences").FindOne(c, bson.M{"$or": bson.A{bson.M{"id": experienceID}, bson.M{"slug": experienceID}, bson.M{"_id": experienceID}}}).Decode(&experience)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Experience not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to load experience pricing")
		return
	}
	basePrice := number(experience["pricePerPerson"])
	if basePrice == 0 {
		basePrice = number(experience["price"])
	}
	if basePrice <= 0 {
		_ = httpx.Fail(c, 400, "invalid_price", "Experience has no valid server-side price")
		return
	}
	childGuests := number(body["childGuests"])
	total := basePrice * adultGuests
	cur, err := h.db.Collection("pricingrules").Find(c, bson.M{"experienceId": experienceID, "isActive": true})
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to calculate price")
		return
	}
	defer cur.Close(c)
	rules := make([]bson.M, 0)
	if err := cur.All(c, &rules); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to calculate price")
		return
	}
	adjustments := make([]bson.M, 0)
	for _, rule := range rules {
		adjustment := number(rule["adjustmentValue"])
		if text(rule["adjustmentType"]) == "percentage" {
			adjustment = total * adjustment / 100
		}
		total += adjustment
		adjustments = append(adjustments, bson.M{"rule": rule["name"], "type": rule["type"], "amount": adjustment})
	}
	_ = httpx.OK(c, gin.H{"baseTotal": basePrice * adultGuests, "adjustments": adjustments, "finalTotal": total, "breakdown": bson.M{"basePrice": basePrice, "adultGuests": adultGuests, "childGuests": childGuests, "date": date, "isPrivate": body["isPrivate"]}}, "")
}

func (h *Handler) Cart(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	userID := claims(c).UserID
	if c.Request.Method == "GET" {
		var cart bson.M
		err := h.db.Collection("marketplacecarts").FindOne(c, bson.M{"userId": userID}).Decode(&cart)
		if errors.Is(err, mongo.ErrNoDocuments) {
			_ = httpx.OK(c, bson.M{"userId": userID, "items": bson.A{}}, "")
			return
		}
		if err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to fetch cart")
			return
		}
		_ = httpx.OK(c, cart, "")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["productId"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "productId is required")
		return
	}
	productID := text(body["productId"])
	quantity := number(body["quantity"])
	if quantity == 0 {
		quantity = 1
	}
	if quantity < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "quantity must be at least 1")
		return
	}
	productObjectID, ok := oid(productID)
	if !ok {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid product ID")
		return
	}
	var product bson.M
	err := h.db.Collection("products").FindOne(c, bson.M{"_id": productObjectID}).Decode(&product)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Product not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch product")
		return
	}
	if product["isActive"] == false || product["inStock"] == false {
		_ = httpx.Fail(c, 400, "unavailable", "Product is not available")
		return
	}
	var cart bson.M
	err = h.db.Collection("marketplacecarts").FindOne(c, bson.M{"userId": userID}).Decode(&cart)
	if errors.Is(err, mongo.ErrNoDocuments) {
		cart = bson.M{"userId": userID, "items": bson.A{bson.M{"productId": productID, "productName": product["name"], "quantity": quantity, "pricePerUnit": product["price"], "addedAt": time.Now().UTC()}}, "createdAt": time.Now().UTC()}
		result, insertErr := h.db.Collection("marketplacecarts").InsertOne(c, cart)
		if insertErr != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to update cart")
			return
		}
		cart["_id"] = result.InsertedID
		_ = httpx.Created(c, cart, "Item added to cart")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch cart")
		return
	}
	items, _ := cart["items"].(bson.A)
	found := false
	for _, item := range items {
		if row, ok := item.(bson.M); ok && text(row["productId"]) == productID {
			row["quantity"] = number(row["quantity"]) + quantity
			found = true
		}
	}
	if !found {
		items = append(items, bson.M{"productId": productID, "productName": product["name"], "quantity": quantity, "pricePerUnit": product["price"], "addedAt": time.Now().UTC()})
	}
	var updated bson.M
	err = h.db.Collection("marketplacecarts").FindOneAndUpdate(c, bson.M{"userId": userID}, bson.M{"$set": bson.M{"items": items, "updatedAt": time.Now().UTC()}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update cart")
		return
	}
	_ = httpx.OK(c, updated, "Cart updated")
}

func (h *Handler) RemoveFromCart(c *gin.Context) {
	if claims(c) == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || text(body["productId"]) == "" {
		_ = httpx.Fail(c, 400, "invalid_request", "productId is required")
		return
	}
	var cart bson.M
	err := h.db.Collection("marketplacecarts").FindOne(c, bson.M{"userId": claims(c).UserID}).Decode(&cart)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Cart not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch cart")
		return
	}
	items, _ := cart["items"].(bson.A)
	kept := make(bson.A, 0, len(items))
	for _, item := range items {
		if row, ok := item.(bson.M); !ok || text(row["productId"]) != text(body["productId"]) {
			kept = append(kept, item)
		}
	}
	var updated bson.M
	err = h.db.Collection("marketplacecarts").FindOneAndUpdate(c, bson.M{"userId": claims(c).UserID}, bson.M{"$set": bson.M{"items": kept, "updatedAt": time.Now().UTC()}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update cart")
		return
	}
	_ = httpx.OK(c, updated, "Item removed from cart")
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
