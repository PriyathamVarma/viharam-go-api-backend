package propertiespg

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/middleware"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{ db *database.Postgres }

func NewHandler(db *database.Postgres) *Handler { return &Handler{db: db} }

const propertySelect = `SELECT id::text,host_id::text,name,description,type,status,city,country,COALESCE(address,''),latitude,longitude,price_per_night_paise,max_guests,bedrooms,bathrooms,COALESCE(image,''),images,amenities,rating,review_count,created_at::text,updated_at::text FROM properties`

func (h *Handler) List(c *gin.Context) {
	page, limit := positive(c.Query("page"), 1), positive(c.Query("limit"), 12)
	if limit > 100 {
		limit = 100
	}
	where, args := []string{"status = 'active'"}, []any{}
	next := 1
	if city := strings.TrimSpace(c.Query("city")); city != "" {
		where = append(where, fmt.Sprintf("city ILIKE $%d", next))
		args = append(args, "%"+city+"%")
		next++
	}
	if typ := strings.TrimSpace(c.Query("type")); typ != "" {
		where = append(where, fmt.Sprintf("type = $%d", next))
		args = append(args, typ)
		next++
	}
	query := propertySelect + " WHERE " + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", next, next+1)
	args = append(args, limit, (page-1)*limit)
	rows, err := h.db.Pool.Query(c, query, args...)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch properties")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, scanErr := scan(rows)
		if scanErr != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode properties")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch properties")
		return
	}
	meta := gin.H{"page": page, "limit": limit}
	if c.Query("includeTotal") != "false" {
		var total int
		if err := h.db.Pool.QueryRow(c, "SELECT count(*) FROM properties WHERE "+strings.Join(where, " AND "), args[:len(args)-2]...).Scan(&total); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to count properties")
			return
		}
		meta["total"] = total
		meta["totalPages"] = int(math.Ceil(float64(total) / float64(limit)))
	}
	_ = httpx.OK(c, gin.H{"items": items, "meta": meta}, "")
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
	name, description, city, country := text(body["name"]), text(body["description"]), text(body["city"]), text(body["country"])
	price, guests := number(body["pricePerNight"]), number(body["maxGuests"])
	if name == "" || description == "" || city == "" || country == "" || price <= 0 || guests < 1 {
		_ = httpx.Fail(c, 400, "invalid_request", "name, description, city, country, pricePerNight and maxGuests are required")
		return
	}
	var id string
	query := `INSERT INTO properties(host_id,name,description,type,status,city,country,address,latitude,longitude,price_per_night_paise,max_guests,bedrooms,bathrooms,image,images,amenities)
VALUES($1,$2,$3,COALESCE(NULLIF($4,''),'hotel'),COALESCE(NULLIF($5,''),'active'),$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
RETURNING id::text`
	err := h.db.Pool.QueryRow(c, query, claims.UserID, name, description, text(body["type"]), text(body["status"]), city, country, text(body["address"]), nullableFloat(body["latitude"]), nullableFloat(body["longitude"]), int64(math.Round(price*100)), int(guests), nullableInt(body["bedrooms"]), nullableInt(body["bathrooms"]), text(body["image"]), jsonBytes(body["images"]), jsonBytes(body["amenities"])).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to create property")
		return
	}
	item, err := h.get(c, id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch created property")
		return
	}
	_ = httpx.Created(c, item, "Property created")
}

func (h *Handler) Get(c *gin.Context) {
	item, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid property ID")
		return
	}
	_ = httpx.OK(c, item, "")
}

func (h *Handler) Update(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	existing, err := h.get(c, c.Param("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid property ID")
		return
	}
	if existing["hostId"] != claims.UserID && claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = httpx.Fail(c, 400, "invalid_request", "Invalid request body")
		return
	}
	fields := map[string]string{"name": "name", "description": "description", "type": "type", "status": "status", "city": "city", "country": "country", "address": "address", "image": "image"}
	sets, args := []string{}, []any{}
	n := 1
	for key, column := range fields {
		if value, ok := body[key]; ok {
			sets = append(sets, fmt.Sprintf("%s=$%d", column, n))
			args = append(args, text(value))
			n++
		}
	}
	if value, ok := body["pricePerNight"]; ok {
		sets = append(sets, fmt.Sprintf("price_per_night_paise=$%d", n))
		args = append(args, int64(math.Round(number(value)*100)))
		n++
	}
	if len(sets) == 0 {
		_ = httpx.OK(c, existing, "Property updated")
		return
	}
	args = append(args, c.Param("id"))
	var id string
	err = h.db.Pool.QueryRow(c, "UPDATE properties SET "+strings.Join(sets, ",")+",updated_at=now() WHERE id=$"+strconv.Itoa(n)+"::uuid RETURNING id::text", args...).Scan(&id)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to update property")
		return
	}
	updated, _ := h.get(c, id)
	_ = httpx.OK(c, updated, "Property updated")
}

func (h *Handler) Delete(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
		return
	}
	var hostID string
	err := h.db.Pool.QueryRow(c, "SELECT host_id::text FROM properties WHERE id=$1::uuid", c.Param("id")).Scan(&hostID)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = httpx.Fail(c, 404, "not_found", "Property not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 400, "invalid_id", "Invalid property ID")
		return
	}
	if hostID != claims.UserID && claims.Role != "Admin" && claims.Role != "SuperAdmin" {
		_ = httpx.Fail(c, 403, "forbidden", "Forbidden")
		return
	}
	if _, err := h.db.Pool.Exec(c, "DELETE FROM properties WHERE id=$1::uuid", c.Param("id")); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to delete property")
		return
	}
	_ = httpx.OK(c, nil, "Property deleted")
}

func (h *Handler) get(c *gin.Context, id string) (map[string]any, error) {
	row := h.db.Pool.QueryRow(c, propertySelect+" WHERE id=$1::uuid", id)
	return scan(row)
}
func scan(row pgx.Row) (map[string]any, error) {
	var id, hostID, name, description, typ, status, city, country, address, image, created, updated string
	var lat, lon *float64
	var price int64
	var guests int
	var bedrooms, bathrooms *int
	var images, amenities []byte
	var rating float64
	var reviews int
	if err := row.Scan(&id, &hostID, &name, &description, &typ, &status, &city, &country, &address, &lat, &lon, &price, &guests, &bedrooms, &bathrooms, &image, &images, &amenities, &rating, &reviews, &created, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"_id": id, "hostId": hostID, "name": name, "description": description, "type": typ, "status": status, "city": city, "country": country, "address": address, "latitude": lat, "longitude": lon, "pricePerNight": float64(price) / 100, "maxGuests": guests, "bedrooms": bedrooms, "bathrooms": bathrooms, "image": image, "images": decodeJSON(images), "amenities": decodeJSON(amenities), "rating": rating, "reviewCount": reviews, "createdAt": created, "updatedAt": updated}, nil
}
func decodeJSON(raw []byte) any {
	if len(raw) == 0 {
		return []any{}
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return []any{}
	}
	return value
}
func jsonBytes(value any) []byte {
	if value == nil {
		return []byte("[]")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("[]")
	}
	return raw
}
func text(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		x, _ := n.Float64()
		return x
	}
	return 0
}
func nullableFloat(v any) any {
	if number(v) == 0 {
		return nil
	}
	return number(v)
}
func nullableInt(v any) any {
	if number(v) == 0 {
		return nil
	}
	return int(number(v))
}
func positive(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}
