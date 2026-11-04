package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/viharam/go-api-backend/internal/platform/httpx"
)

type Claims struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	Role   string `json:"type"`
	jwt.RegisteredClaims
}

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, _ := c.Cookie("token")
		if raw == "" {
			raw = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		}
		if raw == "" {
			_ = httpx.Fail(c, 401, "unauthorized", "Authentication required")
			c.Abort()
			return
		}
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			_ = httpx.Fail(c, 401, "unauthorized", "Invalid or expired token")
			c.Abort()
			return
		}
		c.Set("claims", claims)
		c.Next()
	}
}
func ClaimsFrom(c *gin.Context) *Claims {
	value, _ := c.Get("claims")
	claims, _ := value.(*Claims)
	return claims
}
