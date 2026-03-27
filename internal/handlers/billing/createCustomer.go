package billing

import (
	"log/slog"
	"net/http"

	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v84"
)

func CreateCustomer(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	pool := c.MustGet("Pool").(*pgxpool.Pool)
	stripeClient := c.MustGet("StripeClient").(*stripe.Client)
	ctx := c.Request.Context()

	// Check if the user already has a Stripe customer ID
	customersList := stripeClient.V1Customers.List(ctx, &stripe.CustomerListParams{
		Email: stripe.String(userClaims.User.Email),
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
	if existingCustomer != nil {
		c.JSON(http.StatusConflict, gin.H{
			"customer": existingCustomer,
			"message":  "Stripe customer already exists for this user",
		})
		return
	}

	// Create a new Stripe customer
	customerParams := &stripe.CustomerCreateParams{
		Email: stripe.String(userClaims.User.Email),
	}
	customer, err := stripeClient.V1Customers.Create(ctx, customerParams)
	if err != nil {
		slog.Error("Failed to create Stripe customer", "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create Stripe customer",
			"message": err.Error(),
		})
		return
	}

	// Store the new Stripe customer ID in the database
	_, err = pool.Exec(c.Request.Context(), `UPDATE users SET stripe_customer_id=$1 WHERE id=$2`, customer.ID, userClaims.User.Id)
	if err != nil {
		slog.Error("Failed to store Stripe customer ID in database", "user_id", userClaims.User.Id, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to store Stripe customer ID",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"customer": customer,
		"message":  "Stripe customer created successfully",
	})
}
