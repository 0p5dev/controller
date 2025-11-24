package models

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Deployment struct {
	Id             string `json:"id"`
	Name           string `json:"name"`
	Url            string `json:"url"`
	ContainerImage string `json:"container_image"`
	User           string `json:"user"`
	MinInstances   int    `json:"min_instances"`
	MaxInstances   int    `json:"max_instances"`
}

func MigrateDeploymentTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS deployments (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			container_image TEXT NOT NULL REFERENCES container_images(fqin),
			user TEXT NOT NULL,
			min_instances INT NOT NULL DEFAULT 0,
			max_instances INT NOT NULL DEFAULT 1
		);
	`)
	return err
}
