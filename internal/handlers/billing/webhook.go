package billing

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/webhook"
)

func Webhook(c *gin.Context) {
	payload, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	event, err := webhook.ConstructEvent(payload, c.GetHeader("Stripe-Signature"), "endpoint-secret-placeholder")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook signature"})
		return
	}

	// Handle the event
	switch event.Type {
	case "payment_intent.succeeded":
		var paymentIntent stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &paymentIntent)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse event data"})
			return
		}
		slog.Info("Payment intent succeeded", "payment_intent", paymentIntent)
		// TODO: Handle successful payment intent (e.g., update database, send email, etc.)
	case "payment_method.attached":
		var paymentMethod stripe.PaymentMethod
		err := json.Unmarshal(event.Data.Raw, &paymentMethod)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse event data"})
			return
		}
		slog.Info("Payment method attached", "payment_method", paymentMethod)
		// TODO: Handle attached payment method (e.g., update database, send email, etc.)
	default:
		// Unexpected event type
		c.JSON(http.StatusBadRequest, gin.H{"error": "unexpected event type"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
