package sharedUtils

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/0p5dev/controller/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v84"
)

func HashEmail(email string) string {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	hashedEmail := sha256.Sum256([]byte(normalizedEmail))
	return hex.EncodeToString(hashedEmail[:])[:16] // Use first 16 chars of hash for uniqueness and obfuscation
}

func ValidateMinAndMaxInstances(min *int, max *int) (int, int) {
	effectiveMin := 0
	effectiveMax := 1

	if min != nil {
		effectiveMin = *min
	}
	if effectiveMin < 0 {
		effectiveMin = 0
	}
	if effectiveMin > 10 {
		effectiveMin = 10
	}
	if max != nil {
		effectiveMax = *max
	}
	if effectiveMax < effectiveMin {
		effectiveMax = effectiveMin
	}
	if effectiveMax > 10 {
		effectiveMax = 10
	}

	return effectiveMin, effectiveMax
}

func SucceedProvisioningJob(ctx context.Context, pool *pgxpool.Pool, jobId string) {
	_, execErr := pool.Exec(ctx, "UPDATE provisioning_jobs SET status = 'succeeded', completed_at = NOW() WHERE id = $1", jobId)
	if execErr != nil {
		slog.Error("Failed to update provisioning job status", "job_id", jobId, "error", execErr.Error())
	}
}

func FailProvisioningJob(ctx context.Context, pool *pgxpool.Pool, jobId string, errMsg string) {
	slog.Error("Provisioning job failed", "job_id", jobId, "error", errMsg)
	_, execErr := pool.Exec(ctx, "UPDATE provisioning_jobs SET status = 'failed', completed_at = NOW() WHERE id = $1", jobId)
	if execErr != nil {
		slog.Error("Failed to update provisioning job status", "job_id", jobId, "error", execErr.Error())
	}
}

func GetOrCreateUser(pool *pgxpool.Pool, oauthClaims OauthClaims, stripeClient *stripe.Client) (models.User, error) {
	ctx := context.Background()
	var user models.User

	err := pool.QueryRow(ctx, `SELECT id, email, stripe_customer_id, stripe_payment_method_id, last_billed_at, created_at, updated_at FROM users WHERE email=$1`, oauthClaims.Email).Scan(&user.Id, &user.Email, &user.StripeCustomer_Id, &user.StripePaymentMethodId, &user.LastBilledAt, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			customersList := stripeClient.V1Customers.List(ctx, &stripe.CustomerListParams{
				Email: stripe.String(oauthClaims.Email),
			})
			var existingCustomer *stripe.Customer
			for customer, err := range customersList {
				if err != nil {
					slog.Error("Failed to list Stripe customers", "error", err.Error())
					return models.User{}, fmt.Errorf("failed to list Stripe customers: %v", err)
				}
				existingCustomer = customer
				break
			}
			if existingCustomer != nil {
				return models.User{}, fmt.Errorf("Stripe customer already exists for this user")
			}

			// Create a new Stripe customer
			customerParams := &stripe.CustomerCreateParams{
				Email: stripe.String(oauthClaims.Email),
			}
			customer, err := stripeClient.V1Customers.Create(ctx, customerParams)
			if err != nil {
				slog.Error("Failed to create Stripe customer", "error", err.Error())
				return models.User{}, fmt.Errorf("failed to create Stripe customer: %v", err)
			}

			err = pool.QueryRow(ctx, `
				INSERT INTO users (email, stripe_customer_id)
				VALUES ($1, $2)
				RETURNING id, email, stripe_customer_id, stripe_payment_method_id, last_billed_at, created_at, updated_at
			`, oauthClaims.Email, customer.ID).Scan(&user.Id, &user.Email, &user.StripeCustomer_Id, &user.StripePaymentMethodId, &user.LastBilledAt, &user.CreatedAt, &user.UpdatedAt)
			if err != nil {
				slog.Error("Failed to create user in database", "email", oauthClaims.Email, "error", err.Error())
				return models.User{}, fmt.Errorf("failed to create user in database: %v", err)
			}
			slog.Info("Created new user in database", "email", oauthClaims.Email)
		} else {
			return models.User{}, fmt.Errorf("failed to fetch user from database: %v", err)
		}
	}

	return user, nil
}
