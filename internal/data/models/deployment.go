package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Deployment struct {
	Id             string    `json:"id"`
	Name           string    `json:"name"`
	Url            string    `json:"url"`
	ContainerImage string    `json:"container_image"`
	UserEmail      string    `json:"user_email"`
	MinInstances   int       `json:"min_instances"`
	MaxInstances   int       `json:"max_instances"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func MigrateDeploymentTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS deployments (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			container_image TEXT NOT NULL REFERENCES container_images(fqin),
			user_email TEXT NOT NULL,
			min_instances INT NOT NULL DEFAULT 0,
			max_instances INT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}
