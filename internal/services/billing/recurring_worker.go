package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
	"github.com/stripe/stripe-go/v84"
)

const (
	recurringBillingCronSpec = "*/5 * * * *"
	dummyUsageAmountCents    = int64(50)
	usageCurrency            = "usd"
	billingInterval          = 5 * time.Minute
)

type recurringBillingWorker struct {
	pool         *pgxpool.Pool
	stripeClient *stripe.Client
	cron         *cron.Cron
}

var (
	workerLifecycleMu sync.Mutex
	activeWorker      *recurringBillingWorker
)

func StartRecurringBillingWorker(pool *pgxpool.Pool, stripeAPIKey string) error {
	workerLifecycleMu.Lock()
	defer workerLifecycleMu.Unlock()

	if activeWorker != nil {
		slog.Info("Recurring billing worker already running")
		return nil
	}

	if pool == nil {
		return errors.New("pool is required")
	}

	if stripeAPIKey == "" {
		return errors.New("stripe api key is required")
	}

	worker := &recurringBillingWorker{
		pool:         pool,
		stripeClient: stripe.NewClient(stripeAPIKey),
		cron: cron.New(
			cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
		),
	}

	entryID, err := worker.cron.AddFunc(recurringBillingCronSpec, func() {
		worker.runBillingCycle(context.Background())
	})
	if err != nil {
		return fmt.Errorf("failed to schedule recurring billing worker: %w", err)
	}

	worker.cron.Start()
	activeWorker = worker

	nextRun := worker.cron.Entry(entryID).Next
	slog.Info("Started recurring billing worker", "schedule", recurringBillingCronSpec, "next_run", nextRun.Format(time.RFC3339))
	return nil
}

func StopRecurringBillingWorker(ctx context.Context) error {
	workerLifecycleMu.Lock()
	worker := activeWorker
	activeWorker = nil
	workerLifecycleMu.Unlock()

	if worker == nil {
		slog.Info("Recurring billing worker was not running")
		return nil
	}

	stopCtx := worker.cron.Stop()

	select {
	case <-stopCtx.Done():
		slog.Info("Stopped recurring billing worker")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for recurring billing worker to stop: %w", ctx.Err())
	}
}

func (w *recurringBillingWorker) runBillingCycle(ctx context.Context) {
	startedAt := time.Now().UTC()
	billingWindowStart := startedAt.Truncate(billingInterval)
	slog.Info("Recurring billing cycle started", "started_at", startedAt.Format(time.RFC3339))

	rows, err := w.pool.Query(ctx, `
		SELECT id, stripe_customer_id, stripe_payment_method_id
		FROM users
		WHERE stripe_customer_id IS NOT NULL
		  AND stripe_payment_method_id IS NOT NULL
		  AND (last_billed_at IS NULL OR last_billed_at < $1)
	`, billingWindowStart)
	if err != nil {
		slog.Error("Recurring billing cycle failed while selecting users", "error", err)
		return
	}
	defer rows.Close()

	processedUsers := 0
	billedUsers := 0
	failedUsers := 0

	for rows.Next() {
		processedUsers++

		var userID string
		var stripeCustomerID string
		var stripePaymentMethodID string
		if err := rows.Scan(&userID, &stripeCustomerID, &stripePaymentMethodID); err != nil {
			failedUsers++
			slog.Error("Failed to scan user row for recurring billing", "error", err)
			continue
		}

		if err := w.billUser(ctx, userID, stripeCustomerID, billingWindowStart); err != nil {
			failedUsers++
			slog.Error("Recurring billing user charge failed", "user_id", userID, "stripe_customer_id", stripeCustomerID, "payment_method_id", stripePaymentMethodID, "error", err)
			continue
		}

		billedUsers++
	}

	if err := rows.Err(); err != nil {
		slog.Error("Recurring billing cycle row iteration failed", "error", err)
	}

	slog.Info(
		"Recurring billing cycle completed",
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"processed_users", processedUsers,
		"billed_users", billedUsers,
		"failed_users", failedUsers,
		"billing_window_start", billingWindowStart.Format(time.RFC3339),
	)
}

func (w *recurringBillingWorker) billUser(ctx context.Context, userID string, stripeCustomerID string, billingWindowStart time.Time) error {
	amountCents := getDummyUsageAmountCents()
	idempotencyKeyPrefix := fmt.Sprintf("usage-billing:%s:%d", userID, billingWindowStart.Unix())

	invoiceItemParams := &stripe.InvoiceItemCreateParams{
		Customer:    stripe.String(stripeCustomerID),
		Amount:      stripe.Int64(amountCents),
		Currency:    stripe.String(usageCurrency),
		Description: stripe.String("Cloud Run usage (dummy test data, 5-minute recurring bill)"),
		Metadata: map[string]string{
			"billing_window_start": billingWindowStart.Format(time.RFC3339),
			"user_id":              userID,
		},
	}
	invoiceItemParams.Params.IdempotencyKey = stripe.String(idempotencyKeyPrefix + ":invoice-item")

	_, err := w.stripeClient.V1InvoiceItems.Create(ctx, invoiceItemParams)
	if err != nil {
		return fmt.Errorf("failed to create invoice item: %w", err)
	}

	invoiceCreateParams := &stripe.InvoiceCreateParams{
		Customer:                    stripe.String(stripeCustomerID),
		AutoAdvance:                 stripe.Bool(false),
		CollectionMethod:            stripe.String(string(stripe.InvoiceCollectionMethodChargeAutomatically)),
		PendingInvoiceItemsBehavior: stripe.String("include"),
		Description:                 stripe.String("Automated recurring usage invoice (dummy test data)"),
		Metadata: map[string]string{
			"billing_window_start": billingWindowStart.Format(time.RFC3339),
			"user_id":              userID,
		},
	}
	invoiceCreateParams.Params.IdempotencyKey = stripe.String(idempotencyKeyPrefix + ":invoice-create")

	invoice, err := w.stripeClient.V1Invoices.Create(ctx, invoiceCreateParams)
	if err != nil {
		return fmt.Errorf("failed to create invoice: %w", err)
	}

	invoiceFinalizeParams := &stripe.InvoiceFinalizeInvoiceParams{}
	invoiceFinalizeParams.Params.IdempotencyKey = stripe.String(idempotencyKeyPrefix + ":invoice-finalize")

	_, err = w.stripeClient.V1Invoices.FinalizeInvoice(ctx, invoice.ID, invoiceFinalizeParams)
	if err != nil {
		return fmt.Errorf("failed to finalize invoice %s: %w", invoice.ID, err)
	}

	invoicePayParams := &stripe.InvoicePayParams{}
	invoicePayParams.Params.IdempotencyKey = stripe.String(idempotencyKeyPrefix + ":invoice-pay")

	paidInvoice, err := w.stripeClient.V1Invoices.Pay(ctx, invoice.ID, invoicePayParams)
	if err != nil {
		return fmt.Errorf("failed to pay invoice %s: %w", invoice.ID, err)
	}

	if err := w.recordSuccessfulBilling(ctx, userID, amountCents, billingWindowStart); err != nil {
		return fmt.Errorf("failed to persist successful billing for invoice %s: %w", paidInvoice.ID, err)
	}

	slog.Info(
		"Recurring billing user charge succeeded",
		"user_id", userID,
		"stripe_customer_id", stripeCustomerID,
		"invoice_id", paidInvoice.ID,
		"amount_cents", amountCents,
	)

	return nil
}

func (w *recurringBillingWorker) recordSuccessfulBilling(ctx context.Context, userID string, amountCents int64, billingWindowStart time.Time) error {
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	updateTag, err := tx.Exec(ctx, `
		UPDATE users
		SET last_billed_at = $2
		WHERE id = $1
		  AND (last_billed_at IS NULL OR last_billed_at < $2)
	`, userID, billingWindowStart)
	if err != nil {
		return err
	}

	if updateTag.RowsAffected() == 0 {
		slog.Warn(
			"Skipping duplicate billing persistence for current billing window",
			"user_id", userID,
			"billing_window_start", billingWindowStart.Format(time.RFC3339),
		)
		return tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO usage_ledger (user_id, amount_cents, recorded_at)
		VALUES ($1, $2, $3)
	`, userID, amountCents, billingWindowStart)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func getDummyUsageAmountCents() int64 {
	return dummyUsageAmountCents
}
