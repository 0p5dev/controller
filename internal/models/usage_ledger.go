package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UsageLedger struct {
	Id          string    `json:"id"`
	UserId      string    `json:"user_id"`
	AmountCents int64     `json:"amount_cents"`
	RecordedAt  time.Time `json:"recorded_at"`
}

func MigrateUsageLedgerTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS usage_ledger (
			id VARCHAR(26) PRIMARY KEY,
			user_id VARCHAR(26) NOT NULL REFERENCES users(id),
			amount_cents BIGINT NOT NULL,
			recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}
