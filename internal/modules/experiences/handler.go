package experiences

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"strconv"
	"strings"
)

type Handler struct{ repo *Repository }

func NewHandler(repo *Repository) *Handler { return &Handler{repo: repo} }
func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(queryOr(c, "page", "1"))
	limit, _ := strconv.Atoi(queryOr(c, "limit", "12"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 12
	}
	featured, _ := strconv.ParseBool(c.Query("isFeatured"))
	includeTotal := c.Query("includeTotal") != "false"
	items, total, err := h.repo.List(c.Request.Context(), SearchQuery{Q: strings.TrimSpace(c.Query("q")), Category: c.Query("category"), City: c.Query("city"), State: c.Query("state"), Featured: featured, Page: page, Limit: limit, IncludeTotal: includeTotal})
	if err != nil {
		_ = httpx.Fail(c, 500, "internal_error", "Could not load experiences")
		return
	}
	_ = httpx.OK(c, gin.H{"experiences": items, "total": total, "page": page, "limit": limit}, "Experiences")
}

func (h *Handler) Detail(c *gin.Context) {
	item, err := h.repo.Get(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err == pgx.ErrNoRows {
		_ = httpx.Fail(c, 404, "not_found", "Experience not found")
		return
	}
	if err != nil {
		_ = httpx.Fail(c, 500, "internal_error", "Could not load experience")
		return
	}
	_ = httpx.OK(c, item, "")
}

func queryOr(c *gin.Context, key, fallback string) string {
	value := c.Query(key)
	if value == "" {
		return fallback
	}
	return value
}
