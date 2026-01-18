package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"fmt"
	"os"
	"strings"

	sharedtypes "github.com/0p5dev/controller/pkg/sharedTypes"
	"github.com/golang-jwt/jwt/v5"
)

func getUserClaims(authHeader string) (*sharedtypes.UserClaims, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("authorization header required")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("authorization header must contain Bearer token")
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	token, err := jwt.ParseWithClaims(tokenString, &sharedtypes.UserClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %v", err)
	}

	userClaims, ok := token.Claims.(*sharedtypes.UserClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return userClaims, nil
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		userClaims, err := getUserClaims(authHeader)
		if err != nil {
			slog.Error("Failed to authenticate user", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: " + err.Error(),
			})
			return
		}
		c.Set("userClaims", userClaims)
		c.Next()
	}
}
