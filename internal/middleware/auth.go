package middleware

import (
	"log/slog"
	"net/http"

	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v84"

	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

func getUserClaims(authHeader string, pool *pgxpool.Pool, stripeClient *stripe.Client) (*sharedUtils.UserClaims, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("authorization header required")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("authorization header must contain Bearer token")
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	token, err := jwt.ParseWithClaims(tokenString, &sharedUtils.OauthClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %v", err)
	}

	oauthClaims, ok := token.Claims.(*sharedUtils.OauthClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	user, err := sharedUtils.GetOrCreateUser(pool, *oauthClaims, stripeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create user: %v", err)
	}

	userClaims := &sharedUtils.UserClaims{
		OauthClaims: *oauthClaims,
		// User:        user,
	}

	if userClaims.UserMetadata.AppUser == nil {
		userClaims.UserMetadata.AppUser = &user
	}

	return userClaims, nil
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		pool := c.MustGet("Pool").(*pgxpool.Pool)
		stripeClient := c.MustGet("StripeClient").(*stripe.Client)
		authHeader := c.GetHeader("Authorization")

		userClaims, err := getUserClaims(authHeader, pool, stripeClient)
		if err != nil {
			slog.Error("Failed to authenticate user", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: " + err.Error(),
			})
			return
		}

		// slog.Info("Authenticated user", "claims", userClaims)

		c.Set("UserClaims", userClaims)
		c.Next()
	}
}
