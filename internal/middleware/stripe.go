package middleware

import (
	"os"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v84"
)

func StripeMiddleware() gin.HandlerFunc {
	stripeClient := stripe.NewClient(os.Getenv("STRIPE_API_KEY"))

	return func(c *gin.Context) {
		c.Set("StripeClient", stripeClient)
		c.Next()
	}
}
