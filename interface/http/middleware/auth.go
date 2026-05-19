package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

func APIKeyAuth(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		got := c.GetHeader("X-Api-Key")
		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
				"code":  "UNAUTHORIZED",
			})
			return
		}
		c.Next()
	}
}
