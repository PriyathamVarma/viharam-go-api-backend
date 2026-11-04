package catalogpg

import (
	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) Categories(c *gin.Context) {
	_ = httpx.OK(c, gin.H{"categories": []gin.H{
		{"slug": "food-and-farming", "name": "Food & Farming"},
		{"slug": "crafts-and-artisans", "name": "Crafts & Artisans"},
		{"slug": "nature-and-wildlife", "name": "Nature & Wildlife"},
		{"slug": "culture-and-heritage", "name": "Culture & Heritage"},
		{"slug": "wellness-and-spirituality", "name": "Wellness & Spirituality"},
		{"slug": "adventure-and-outdoors", "name": "Adventure & Outdoors"},
		{"slug": "community-and-lifestyle", "name": "Community & Lifestyle"},
		{"slug": "festivals-and-events", "name": "Festivals & Events"},
	}}, "")
}
