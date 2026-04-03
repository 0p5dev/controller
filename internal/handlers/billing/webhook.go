package billing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/webhook"
)

func Webhook(c *gin.Context) {
	pool := c.MustGet("Pool").(*pgxpool.Pool)
	stripeClient := c.MustGet("StripeClient").(*stripe.Client)

	payload, err := c.GetRawData()
	if err != nil {
		slog.Error("Failed to read webhook payload", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	event, err := webhook.ConstructEvent(payload, c.GetHeader("Stripe-Signature"), os.Getenv("STRIPE_WEBHOOK_SIGNING_SECRET"))
	if err != nil {
		slog.Error("Failed to verify webhook signature", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook signature"})
		return
	}

	switch event.Type {
	case "setup_intent.succeeded":
		handleSetupIntentSuccess(c, event, pool, stripeClient)
	case "payment_method.attached":
		handlePaymentMethodAttached(c, event)
	default:
		slog.Info("Unexpected Stripe webhook event type", "event_type", event.Type)
		c.JSON(http.StatusAccepted, gin.H{"error": "unexpected event type"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func handleSetupIntentSuccess(c *gin.Context, event stripe.Event, pool *pgxpool.Pool, stripeClient *stripe.Client) {
	ctx := context.Background()

	var setupIntent stripe.SetupIntent
	err := json.Unmarshal(event.Data.Raw, &setupIntent)
	if err != nil {
		slog.Error("Failed to parse setup intent event data", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse event data"})
		return
	}
	// slog.Info("Setup intent succeeded", "payment_method_id", setupIntent.PaymentMethod.ID)

	_, err = pool.Exec(ctx, "UPDATE users SET stripe_payment_method_id = $1 WHERE stripe_customer_id = $2", setupIntent.PaymentMethod.ID, setupIntent.Customer.ID)
	if err != nil {
		slog.Error("Failed to update user with payment method", "customer_id", setupIntent.Customer.ID, "payment_method_id", setupIntent.PaymentMethod.ID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user with payment method"})
		return
	}
	// slog.Info("Updated user with new payment method", "customer_id", setupIntent.Customer.ID, "payment_method_id", setupIntent.PaymentMethod.ID)

	_, err = stripeClient.V1Customers.Update(ctx, setupIntent.Customer.ID, &stripe.CustomerUpdateParams{
		InvoiceSettings: &stripe.CustomerUpdateInvoiceSettingsParams{
			DefaultPaymentMethod: stripe.String(setupIntent.PaymentMethod.ID),
		},
	})
	if err != nil {
		slog.Error("Failed to set default payment method for customer", "customer_id", setupIntent.Customer.ID, "payment_method_id", setupIntent.PaymentMethod.ID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set default payment method for customer"})
		return
	}
	// slog.Info("Set default payment method for customer", "customer_id", setupIntent.Customer.ID, "payment_method_id", setupIntent.PaymentMethod.ID)
}

func handlePaymentMethodAttached(c *gin.Context, event stripe.Event) {
	var paymentMethod stripe.PaymentMethod
	err := json.Unmarshal(event.Data.Raw, &paymentMethod)
	if err != nil {
		slog.Error("Failed to parse payment method event data", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse event data"})
		return
	}
	// slog.Info("Payment method attached", "card", paymentMethod.Card)
	// TODO: Handle attached payment method (e.g., update database, send email, etc.)
}
