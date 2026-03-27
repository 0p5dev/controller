package middleware

import (
	"log/slog"
	"net/http"

	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"

	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

func getUserClaims(authHeader string) (*sharedUtils.UserClaims, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("authorization header required")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("authorization header must contain Bearer token")
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	token, err := jwt.ParseWithClaims(tokenString, &sharedUtils.UserClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %v", err)
	}

	userClaims, ok := token.Claims.(*sharedUtils.UserClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	userClaims.UserMetadata["stripe_customer_id"] = nil

	return userClaims, nil
}

// func getStripeCustomerID(email string) (string, error) {
// customersList := app.StripeClient.V1Customers.List(ctx, &stripe.CustomerListParams{
// 	Email: stripe.String(userClaims.Email),
// })
// var existingCustomer *stripe.Customer
// for customer, err := range customersList {
// 	if err != nil {
// 		slog.Error("Failed to list Stripe customers", "error", err.Error())
// 		c.JSON(http.StatusInternalServerError, gin.H{
// 			"error":   "failed to list Stripe customers",
// 			"message": err.Error(),
// 		})
// 		return
// 	}
// 	existingCustomer = customer
// 	break
// }
// if existingCustomer != nil {
// 	c.JSON(http.StatusConflict, gin.H{
// 		"customer": existingCustomer,
// 		"message":  "Stripe customer already exists for this user",
// 	})
// 	return
// }
// }

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
		slog.Info("Authenticated user", "claims", userClaims)
		c.Set("UserClaims", userClaims)
		c.Next()
	}
}
