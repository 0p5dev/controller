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
	UserId         string    `json:"user_id"`
	MinInstances   int       `json:"min_instances"`
	MaxInstances   int       `json:"max_instances"`
	Port           int       `json:"port"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func MigrateDeploymentTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS deployments (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			container_image TEXT NOT NULL REFERENCES container_images(fqin),
			user_id UUID NOT NULL REFERENCES users(id),
			min_instances INT NOT NULL DEFAULT 0,
			max_instances INT NOT NULL DEFAULT 1,
			port INT NOT NULL DEFAULT 8080,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}
