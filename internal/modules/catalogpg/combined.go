package catalogpg

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/modules/experiences"
	"github.com/viharam/go-api-backend/internal/platform/database"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type CombinedHandler struct {
	db   *database.Postgres
	exps *experiences.Repository
}

func NewCombinedHandler(db *database.Postgres) *CombinedHandler {
	return &CombinedHandler{db: db, exps: experiences.NewRepository(db)}
}

// List returns the traveller catalogue in one request. It deliberately omits
// total counts because catalogue screens only need the current page.
func (h *CombinedHandler) List(c *gin.Context) {
	limit := 12
	if value := c.Query("limit"); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &limit); err != nil || limit < 1 {
			limit = 12
		}
	}
	if limit > 100 {
		limit = 100
	}

	items, _, err := h.exps.List(c.Request.Context(), experiences.SearchQuery{
		Q: strings.TrimSpace(c.Query("q")), Category: c.Query("category"),
		City: c.Query("city"), State: c.Query("state"), Page: 1,
		Limit: limit, IncludeTotal: false,
	})
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch catalogue")
		return
	}

	rows, err := h.db.Pool.Query(c, `SELECT id::text,name,description,type,city,country,COALESCE(address,''),price_per_night_paise,max_guests,bedrooms,bathrooms,COALESCE(image,''),images,amenities,rating,review_count FROM properties WHERE status='active' ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch catalogue")
		return
	}
	defer rows.Close()
	properties := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, description, typ, city, country, address, image string
		var price int64
		var maxGuests, bedrooms, bathrooms, reviews int
		var images, amenities []byte
		var rating float64
		if err := rows.Scan(&id, &name, &description, &typ, &city, &country, &address, &price, &maxGuests, &bedrooms, &bathrooms, &image, &images, &amenities, &rating, &reviews); err != nil {
			_ = httpx.Fail(c, 500, "database_error", "Failed to decode catalogue")
			return
		}
		var imageList, amenityList any
		_ = json.Unmarshal(images, &imageList)
		_ = json.Unmarshal(amenities, &amenityList)
		properties = append(properties, map[string]any{
			"_id": id, "name": name, "description": description, "type": typ,
			"city": city, "country": country, "address": address,
			"pricePerNight": float64(price) / 100, "maxGuests": maxGuests,
			"bedrooms": bedrooms, "bathrooms": bathrooms, "image": image,
			"images": imageList, "amenities": amenityList, "rating": rating,
			"reviewCount": reviews,
		})
	}
	if err := rows.Err(); err != nil {
		_ = httpx.Fail(c, 500, "database_error", "Failed to fetch catalogue")
		return
	}
	_ = httpx.OK(c, gin.H{"experiences": items, "properties": properties}, "Catalogue")
}
