package data

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0p5dev/controller/internal/data/models"
)

func ListenForProvisioningJobUpdates(onUpdate func(models.ProvisioningJobUpdate)) error {
	ctx := context.Background()
	postgresConnectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	conn, err := pgx.Connect(ctx, postgresConnectionString)
	if err != nil {
		return fmt.Errorf("error making dedicated connection to database for LISTEN/NOTIFY: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "LISTEN provisioning_jobs_updates"); err != nil {
		return fmt.Errorf("LISTEN failed: %w", err)
	}

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err // caller can restart/backoff
		}

		var update models.ProvisioningJobUpdate
		if err := json.Unmarshal([]byte(notification.Payload), &update); err != nil {
			slog.Warn("invalid provisioning_jobs notification payload", "payload", notification.Payload, "error", err)
			continue
		}

		onUpdate(update)
	}
}

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
		{"provisioning_jobs", models.MigrateProvisioningJobTable},
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
