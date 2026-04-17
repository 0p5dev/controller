package billing

import (
	"net/http"

	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v84"
)

func GetUserPaymentMethod(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	stripeClient := c.MustGet("StripeClient").(*stripe.Client)
	ctx := c.Request.Context()

	if userClaims.StripeCustomer_Id == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Stripe customer not found for this user",
		})
		return
	}

	if userClaims.StripePaymentMethodId == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "No payment method found for this user",
		})
		return
	}

	paymentMethod, err := stripeClient.V1Customers.RetrievePaymentMethod(ctx, *userClaims.StripePaymentMethodId, &stripe.CustomerRetrievePaymentMethodParams{
		Customer: userClaims.StripeCustomer_Id,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to retrieve payment method",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, paymentMethod.Card)
}
