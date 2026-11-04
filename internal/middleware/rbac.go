package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
	"strings"
)

func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ClaimsFrom(c)
		if claims == nil {
			_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
			c.Abort()
			return
		}
		for _, role := range roles {
			if strings.EqualFold(claims.Role, role) {
				c.Next()
				return
			}
		}
		_ = httpx.Fail(c, 403, "forbidden", "Insufficient permissions")
		c.Abort()
	}
}
