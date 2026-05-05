package sharedUtils

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	normalizedEmail := NormalizeEmail(email)
	hashedEmail := sha256.Sum256([]byte(normalizedEmail))
	return hex.EncodeToString(hashedEmail[:])[:16] // Use first 16 chars of hash for uniqueness and obfuscation
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
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

type userRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func GetOrCreateUser(pool *pgxpool.Pool, oauthClaims OauthClaims, stripeClient *stripe.Client) (models.User, error) {
	ctx := context.Background()
	normalizedEmail := NormalizeEmail(oauthClaims.Email)

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return models.User{}, fmt.Errorf("failed to begin user provisioning transaction: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			slog.Error("Failed to rollback user provisioning transaction", "email", oauthClaims.Email, "error", rollbackErr.Error())
		}
	}()

	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockKey(normalizedEmail))
	if err != nil {
		return models.User{}, fmt.Errorf("failed to acquire user provisioning lock: %w", err)
	}

	user, err := getUserByEmail(ctx, tx, oauthClaims.Email)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return models.User{}, fmt.Errorf("failed to commit existing user lookup: %w", err)
		}
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, fmt.Errorf("failed to fetch user from database: %w", err)
	}

	stripeCustomer, err := findStripeCustomerByEmail(ctx, stripeClient, oauthClaims.Email)
	if err != nil {
		return models.User{}, err
	}

	if stripeCustomer == nil {
		stripeCustomer, err = createStripeCustomer(ctx, stripeClient, oauthClaims.Email, normalizedEmail)
		if err != nil {
			return models.User{}, err
		}
	}

	user, err = upsertUser(ctx, tx, oauthClaims.Email, stripeCustomer.ID)
	if err != nil {
		slog.Error("Failed to upsert user in database", "email", oauthClaims.Email, "stripe_customer_id", stripeCustomer.ID, "error", err.Error())
		return models.User{}, fmt.Errorf("failed to upsert user in database: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return models.User{}, fmt.Errorf("failed to commit user provisioning transaction: %w", err)
	}

	return user, nil
}

func advisoryLockKey(email string) int64 {
	hashedEmail := sha256.Sum256([]byte(email))
	return int64(binary.BigEndian.Uint64(hashedEmail[:8]))
}

func getUserByEmail(ctx context.Context, q userRowQuerier, email string) (models.User, error) {
	return scanUser(q.QueryRow(ctx, `
		SELECT id, email, stripe_customer_id, stripe_payment_method_id, last_billed_at, created_at, updated_at
		FROM users
		WHERE email = $1
	`, email))
}

func upsertUser(ctx context.Context, tx pgx.Tx, email string, stripeCustomerID string) (models.User, error) {
	return scanUser(tx.QueryRow(ctx, `
		INSERT INTO users (email, stripe_customer_id)
		VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE
		SET stripe_customer_id = COALESCE(users.stripe_customer_id, EXCLUDED.stripe_customer_id)
		RETURNING id, email, stripe_customer_id, stripe_payment_method_id, last_billed_at, created_at, updated_at
	`, email, stripeCustomerID))
}

func scanUser(row pgx.Row) (models.User, error) {
	var user models.User
	err := row.Scan(&user.Id, &user.Email, &user.StripeCustomer_Id, &user.StripePaymentMethodId, &user.LastBilledAt, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func findStripeCustomerByEmail(ctx context.Context, stripeClient *stripe.Client, email string) (*stripe.Customer, error) {
	customersList := stripeClient.V1Customers.List(ctx, &stripe.CustomerListParams{
		Email: stripe.String(email),
	})

	var selectedCustomer *stripe.Customer
	matchCount := 0
	for customer, err := range customersList {
		if err != nil {
			slog.Error("Failed to list Stripe customers", "email", email, "error", err.Error())
			return nil, fmt.Errorf("failed to list Stripe customers: %w", err)
		}
		matchCount++
		if selectedCustomer == nil || customer.Created < selectedCustomer.Created || (customer.Created == selectedCustomer.Created && customer.ID < selectedCustomer.ID) {
			selectedCustomer = customer
		}
	}

	if matchCount > 1 && selectedCustomer != nil {
		slog.Warn("Multiple Stripe customers found for email; reusing the earliest customer", "email", email, "selected_customer_id", selectedCustomer.ID, "match_count", matchCount)
	}

	return selectedCustomer, nil
}

func createStripeCustomer(ctx context.Context, stripeClient *stripe.Client, email string, normalizedEmail string) (*stripe.Customer, error) {
	customerParams := &stripe.CustomerCreateParams{
		Email: stripe.String(email),
	}
	customerParams.SetIdempotencyKey("user-customer-" + HashEmail(normalizedEmail))

	customer, err := stripeClient.V1Customers.Create(ctx, customerParams)
	if err != nil {
		slog.Error("Failed to create Stripe customer", "email", email, "error", err.Error())
		return nil, fmt.Errorf("failed to create Stripe customer: %w", err)
	}

	return customer, nil
}
