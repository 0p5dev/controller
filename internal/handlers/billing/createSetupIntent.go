package billing

import (
	"log/slog"
	"net/http"

	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v84"
)

func CreateSetupIntent(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	stripeClient := c.MustGet("StripeClient").(*stripe.Client)
	ctx := c.Request.Context()

	slog.Info("CreateSetupIntent", "customer", userClaims.UserMetadata.AppUser.Email)
	customersList := stripeClient.V1Customers.List(ctx, &stripe.CustomerListParams{
		Email: stripe.String(userClaims.UserMetadata.AppUser.Email),
	})
	var existingCustomer *stripe.Customer
	for customer, err := range customersList {
		if err != nil {
			slog.Error("Failed to list Stripe customers", "error", err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "failed to list Stripe customers",
				"message": err.Error(),
			})
			return
		}
		existingCustomer = customer
		break
	}
	if existingCustomer == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"customer": existingCustomer,
			"message":  "Stripe customer not found for this user",
		})
		return
	}

	setupIntent, err := stripeClient.V1SetupIntents.Create(c.Request.Context(), &stripe.SetupIntentCreateParams{
		Customer: stripe.String(existingCustomer.ID),
		PaymentMethodTypes: []*string{
			stripe.String("card"),
		},
		Usage: stripe.String(string(stripe.SetupIntentUsageOffSession)),
	})
	if err != nil {
		slog.Error("Failed to create Stripe setup intent", "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create Stripe setup intent",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"setup_intent": setupIntent.ClientSecret,
	})
}
