package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Pinger interface{ Ping(context.Context) error }
type Handler struct {
	db                       Pinger
	started                  time.Time
	version, commit, builtAt string
}

func New(db Pinger) *Handler {
	return &Handler{db: db, started: time.Now(), version: "dev", commit: "unknown", builtAt: "unknown"}
}
func (h *Handler) Live(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok", "uptime": time.Since(h.started).String()})
}
func (h *Handler) Head(c *gin.Context) {
	c.Status(http.StatusOK)
}
func (h *Handler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	checks := gin.H{}
	if h.db == nil {
		checks["postgres"] = "disabled"
	} else if err := h.db.Ping(ctx); err != nil {
		checks["postgres"] = "not_ready"
		c.JSON(503, gin.H{"status": "not_ready", "checks": checks})
		return
	} else {
		checks["postgres"] = "ok"
	}
	c.JSON(200, gin.H{"status": "ready", "checks": checks})
}
func (h *Handler) Version(c *gin.Context) {
	c.JSON(200, gin.H{"version": h.version, "commit": h.commit, "builtAt": h.builtAt})
}
