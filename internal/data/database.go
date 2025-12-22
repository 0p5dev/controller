package data

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0p5dev/controller/internal/data/models"
)

func InitializeDatabase() (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	postgresConnectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	pool, err := pgxpool.New(ctx, postgresConnectionString)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %v", err)
	}

	migrations := []struct {
		name string
		fn   func(*pgxpool.Pool) error
	}{
		{"container_images", models.MigrateContainerImageTable},
		{"deployments", models.MigrateDeploymentTable},
	}

	for _, migration := range migrations {
		slog.Info("Running migration", "name", migration.name)
		if err := migration.fn(pool); err != nil {
			pool.Close()
			return nil, fmt.Errorf("failed to migrate %s table: %v", migration.name, err)
		}
	}

	return pool, nil
}
