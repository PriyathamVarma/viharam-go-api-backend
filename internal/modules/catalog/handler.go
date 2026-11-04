package catalog

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"github.com/viharam/go-api-backend/internal/platform/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Handler struct{ db *mongodb.Database }

func NewHandler(db *mongodb.Database) *Handler { return &Handler{db: db} }

type category struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Description     string `json:"description"`
	Emoji           string `json:"emoji"`
	Icon            string `json:"icon"`
	Color           string `json:"color"`
	ExperienceCount int    `json:"experienceCount"`
	Image           string `json:"image"`
}

var categories = []category{
	{ID: "cat-1", Name: "Farm & Agriculture", Slug: "farm-agriculture", Description: "Harvest paddy, pick fruits, plough fields, and learn traditional farming methods from local farmers.", Emoji: "🌾", Icon: "Sprout", Color: "bg-primary-light text-primary", ExperienceCount: 24, Image: "https://picsum.photos/seed/farming/800/500"},
	{ID: "cat-2", Name: "Spiritual & Temple", Slug: "spiritual-temple", Description: "Join sacred rituals, temple walks, meditation sessions, and spiritual ceremonies at ancient temples.", Emoji: "🛕", Icon: "Sun", Color: "bg-accent-surface text-accent", ExperienceCount: 18, Image: "https://picsum.photos/seed/temple/800/500"},
	{ID: "cat-3", Name: "Village Life", Slug: "village-life", Description: "Experience authentic village routines — bullock cart rides, well drawing, village markets and more.", Emoji: "🏡", Icon: "Home", Color: "bg-primary-light text-primary", ExperienceCount: 31, Image: "https://picsum.photos/seed/village/800/500"},
	{ID: "cat-4", Name: "Food & Cooking", Slug: "food-cooking", Description: "Cook regional recipes with local families using traditional methods, wood fires and fresh local produce.", Emoji: "🍲", Icon: "ChefHat", Color: "bg-status-warning-surface text-status-warning", ExperienceCount: 22, Image: "https://picsum.photos/seed/cooking/800/500"},
	{ID: "cat-5", Name: "Artisans & Crafts", Slug: "artisans-crafts", Description: "Learn pottery, weaving, block printing, wood carving and folk art from master artisans.", Emoji: "🏺", Icon: "Palette", Color: "bg-surface-warm text-accent", ExperienceCount: 16, Image: "https://picsum.photos/seed/pottery/800/500"},
	{ID: "cat-6", Name: "Nature & Eco", Slug: "nature-eco", Description: "Forest treks, river kayaking, bird watching, and eco-farm stays in pristine natural settings.", Emoji: "🌿", Icon: "TreePine", Color: "bg-secondary-subtle text-secondary", ExperienceCount: 19, Image: "https://picsum.photos/seed/nature/800/500"},
	{ID: "cat-7", Name: "Festivals & Rituals", Slug: "festivals-rituals", Description: "Participate in local harvest festivals, folk dances, bonfire rituals and community celebrations.", Emoji: "🎊", Icon: "Sparkles", Color: "bg-primary-light text-brand", ExperienceCount: 11, Image: "https://picsum.photos/seed/festival/800/500"},
	{ID: "cat-8", Name: "Learning & Workshops", Slug: "learning-workshops", Description: "Hands-on workshops in organic farming, natural dyeing, Ayurvedic medicine and traditional knowledge.", Emoji: "📚", Icon: "BookOpen", Color: "bg-status-info-surface text-status-info", ExperienceCount: 14, Image: "https://picsum.photos/seed/workshop/800/500"},
}

func (h *Handler) Categories(c *gin.Context) { _ = httpx.OK(c, gin.H{"categories": categories}, "") }

func (h *Handler) Detail(c *gin.Context) {
	key := c.Param("id")
	var doc bson.M
	err := h.db.Collection("experiences").FindOne(c.Request.Context(), bson.M{"$or": bson.A{bson.M{"id": key}, bson.M{"slug": key}}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_ = httpx.Fail(c, 404, "not_found", "Experience not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch experience")
		return
	}
	_ = httpx.OK(c, doc, "Experience fetched successfully")
}

func (h *Handler) Search(c *gin.Context) {
	page := positive(c.Query("page"), 1)
	limit := positive(c.Query("limit"), 12)
	if limit > 100 {
		limit = 100
	}
	filter := bson.M{"status": "active"}
	if value := strings.TrimSpace(c.Query("category")); value != "" {
		filter["category"] = value
	}
	if value := strings.TrimSpace(c.Query("city")); value != "" {
		filter["city"] = bson.Regex{Pattern: "^" + regexpQuote(value) + "$", Options: "i"}
	}
	if value := strings.TrimSpace(c.Query("state")); value != "" {
		filter["state"] = bson.Regex{Pattern: "^" + regexpQuote(value) + "$", Options: "i"}
	}
	if value := strings.TrimSpace(c.Query("isFeatured")); value == "true" {
		filter["isFeatured"] = true
	}
	if value := strings.TrimSpace(c.Query("familyFriendly")); value == "true" {
		filter["tags"] = bson.M{"$in": bson.A{"family-friendly", "family"}}
	}
	if value := strings.TrimSpace(c.Query("foreignFriendly")); value == "true" {
		filter["languages"] = bson.M{"$in": bson.A{"English", "english"}}
	}
	if value := strings.TrimSpace(c.Query("languages")); value != "" {
		filter["languages"] = bson.M{"$in": splitCSV(value)}
	}
	if value := strings.TrimSpace(c.Query("tags")); value != "" {
		filter["tags"] = bson.M{"$in": splitCSV(value)}
	}
	if value := strings.TrimSpace(c.Query("q")); value != "" {
		pattern := regexpQuote(value)
		filter["$or"] = bson.A{bson.M{"title": bson.Regex{Pattern: pattern, Options: "i"}}, bson.M{"location": bson.Regex{Pattern: pattern, Options: "i"}}, bson.M{"description": bson.Regex{Pattern: pattern, Options: "i"}}, bson.M{"city": bson.Regex{Pattern: pattern, Options: "i"}}, bson.M{"state": bson.Regex{Pattern: pattern, Options: "i"}}, bson.M{"tags": bson.Regex{Pattern: pattern, Options: "i"}}}
	}
	if value := parseFloat(c.Query("minPrice")); value != nil {
		filter["pricePerPerson"] = bson.M{"$gte": *value}
	}
	if value := parseFloat(c.Query("maxPrice")); value != nil {
		mergePriceFilter(filter, "$lte", *value)
	}
	sortBy := c.Query("sortBy")
	sortDoc := bson.D{{Key: "rating", Value: -1}}
	switch sortBy {
	case "price_asc":
		sortDoc = bson.D{{Key: "pricePerPerson", Value: 1}}
	case "price_desc":
		sortDoc = bson.D{{Key: "pricePerPerson", Value: -1}}
	case "popular":
		sortDoc = bson.D{{Key: "reviewCount", Value: -1}}
	}
	cursor, err := h.db.Collection("experiences").Find(c.Request.Context(), filter, mongooptions.Find().SetSort(sortDoc))
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to perform search")
		return
	}
	defer cursor.Close(c.Request.Context())
	all := make([]bson.M, 0)
	if err := cursor.All(c.Request.Context(), &all); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to perform search")
		return
	}
	for _, item := range all {
		item["trending"] = item["isFeatured"] == true || number(item["rating"]) >= 4.5
	}
	total := len(all)
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	_ = httpx.OK(c, gin.H{"data": all[start:end], "total": total, "page": page, "totalPages": int(math.Ceil(float64(total) / float64(limit)))}, "Search results fetched successfully")
}

func mergePriceFilter(filter bson.M, operator string, value float64) {
	if current, ok := filter["pricePerPerson"].(bson.M); ok {
		current[operator] = value
	} else {
		filter["pricePerPerson"] = bson.M{operator: value}
	}
}
func splitCSV(value string) []string {
	result := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(strings.ToLower(part)); part != "" {
			result = append(result, part)
		}
	}
	return result
}
func positive(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}
func parseFloat(value string) *float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
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
func regexpQuote(value string) string {
	return strings.NewReplacer(`\`, `\\`, `.`, `\.`, `+`, `\+`, `*`, `\*`, `?`, `\?`, `^`, `\^`, `$`, `\$`, `(`, `\(`, `)`, `\)`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`, `|`, `\|`).Replace(value)
}
