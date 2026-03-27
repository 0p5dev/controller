package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/0p5dev/controller/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func DatabaseMiddleware() gin.HandlerFunc {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	postgresConnectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	pool, err := pgxpool.New(ctx, postgresConnectionString)
	if err != nil {
		slog.Error("unable to create database connection pool", "error", err)
		return func(c *gin.Context) {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error: failed to connect to database"})
		}
	}

	migrations := []struct {
		name string
		fn   func(*pgxpool.Pool) error
	}{
		{"container_images", models.MigrateContainerImageTable},
		{"deployments", models.MigrateDeploymentTable},
		{"provisioning_jobs", models.MigrateProvisioningJobTable},
		{"users", models.MigrateUserTable},
		{"usage_ledger", models.MigrateUsageLedgerTable},
	}

	for _, migration := range migrations {
		slog.Info("Running migration", "name", migration.name)
		if err := migration.fn(pool); err != nil {
			pool.Close()
			slog.Error("failed to migrate table", "table", migration.name, "error", err)
			return func(c *gin.Context) {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error: failed to migrate table " + migration.name})
			}
		}
	}

	return func(c *gin.Context) {
		c.Set("Pool", pool)
		c.Next()
	}
}
