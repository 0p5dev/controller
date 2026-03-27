package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	Id                    string     `json:"id"`
	Email                 string     `json:"email"`
	StripeCustomer_Id     *string    `json:"stripe_customer_id"`
	StripePaymentMethodId *string    `json:"stripe_payment_method_id"`
	LastBilledAt          *time.Time `json:"last_billed_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

func MigrateUserTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email TEXT NOT NULL,
			stripe_customer_id TEXT,
			stripe_payment_method_id TEXT,
			last_billed_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}
