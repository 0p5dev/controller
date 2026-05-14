package middleware

import (
	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
)

func PaymentMethodMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
		if userClaims.UserMetadata.AppUser.StripeCustomer_Id == nil || userClaims.UserMetadata.AppUser.StripePaymentMethodId == nil {
			c.JSON(402, gin.H{
				"error": "Payment method required. Please add a payment method to your account.",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
