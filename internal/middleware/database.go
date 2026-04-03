package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/0p5dev/controller/internal/models"
	billingService "github.com/0p5dev/controller/internal/services/billing"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	databasePoolMu sync.Mutex
	databasePool   *pgxpool.Pool
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

	if isRecurringBillingEnabled() {
		if err := billingService.StartRecurringBillingWorker(pool, os.Getenv("STRIPE_API_KEY")); err != nil {
			closePool(pool)
			slog.Error("failed to start recurring billing worker", "error", err)
			return func(c *gin.Context) {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error: failed to start recurring billing worker"})
			}
		}
	} else {
		slog.Info("Recurring billing worker disabled", "env", "RECURRING_BILLING_ENABLED")
	}

	databasePoolMu.Lock()
	databasePool = pool
	databasePoolMu.Unlock()

	return func(c *gin.Context) {
		c.Set("Pool", pool)
		c.Next()
	}
}

func CloseDatabasePool() {
	databasePoolMu.Lock()
	pool := databasePool
	databasePool = nil
	databasePoolMu.Unlock()

	closePool(pool)
}

func isRecurringBillingEnabled() bool {
	value := os.Getenv("RECURRING_BILLING_ENABLED")
	if value == "" {
		return true
	}

	enabled, err := strconv.ParseBool(value)
	if err != nil {
		slog.Warn("Invalid RECURRING_BILLING_ENABLED value, defaulting to true", "value", value, "error", err)
		return true
	}

	return enabled
}

func closePool(pool *pgxpool.Pool) {
	if pool != nil {
		pool.Close()
	}
}
