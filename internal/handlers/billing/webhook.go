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
	case "customer.created":
		handleCustomerCreated(c, event, pool, stripeClient)
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

func handleCustomerCreated(c *gin.Context, event stripe.Event, pool *pgxpool.Pool, stripeClient *stripe.Client) {
	ctx := context.Background()

	var newCustomer stripe.Customer
	if err := json.Unmarshal(event.Data.Raw, &newCustomer); err != nil {
		slog.Error("Failed to parse customer.created event data", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse event data"})
		return
	}

	if newCustomer.Email == "" {
		slog.Warn("customer.created event has no email; skipping deduplication", "customer_id", newCustomer.ID)
		return
	}

	customersList := stripeClient.V1Customers.List(ctx, &stripe.CustomerListParams{
		Email: stripe.String(newCustomer.Email),
	})

	var allCustomers []*stripe.Customer
	for cust, err := range customersList {
		if err != nil {
			slog.Error("Failed to list Stripe customers for deduplication", "email", newCustomer.Email, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list customers for deduplication"})
			return
		}
		allCustomers = append(allCustomers, cust)
	}

	if len(allCustomers) <= 1 {
		return
	}

	// Elect the canonical customer using the same tiebreaker as findStripeCustomerByEmail:
	// earliest Created timestamp, with the lowest ID breaking ties.
	canonical := allCustomers[0]
	for _, cust := range allCustomers[1:] {
		if cust.Created < canonical.Created || (cust.Created == canonical.Created && cust.ID < canonical.ID) {
			canonical = cust
		}
	}

	slog.Warn("Duplicate Stripe customers detected on customer.created; deduplicating",
		"email", newCustomer.Email,
		"canonical_customer_id", canonical.ID,
		"total_count", len(allCustomers),
	)

	for _, duplicate := range allCustomers {
		if duplicate.ID == canonical.ID {
			continue
		}

		// Update the DB before touching Stripe so the DB is never left with a
		// dangling reference to a deleted customer. If this fails we skip the
		// Stripe deletion entirely for this duplicate.
		_, dbErr := pool.Exec(ctx,
			"UPDATE users SET stripe_customer_id = $1 WHERE stripe_customer_id = $2",
			canonical.ID, duplicate.ID,
		)
		if dbErr != nil {
			slog.Error("Failed to migrate user from duplicate to canonical Stripe customer; skipping Stripe deletion to avoid dangling reference",
				"duplicate_id", duplicate.ID,
				"canonical_id", canonical.ID,
				"error", dbErr,
			)
			continue
		}

		if _, stripeErr := stripeClient.V1Customers.Delete(ctx, duplicate.ID, &stripe.CustomerDeleteParams{}); stripeErr != nil {
			// The DB already points to the canonical customer, so this orphaned
			// Stripe customer is harmless. Log and move on.
			slog.Error("Failed to delete duplicate Stripe customer",
				"duplicate_id", duplicate.ID,
				"canonical_id", canonical.ID,
				"email", newCustomer.Email,
				"error", stripeErr,
			)
			continue
		}

		slog.Info("Deleted duplicate Stripe customer",
			"duplicate_id", duplicate.ID,
			"canonical_id", canonical.ID,
			"email", newCustomer.Email,
		)
	}
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
