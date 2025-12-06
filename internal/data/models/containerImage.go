package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ContainerImage struct {
	Fqin      string    `json:"fqin"`
	UserEmail string    `json:"user_email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func MigrateContainerImageTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS container_images (
			fqin TEXT PRIMARY KEY,
			user_email TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}
