package health

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// @Summary Health check
// @Description Check the health status of the API and database connection
// @Tags health
// @Produce json
// @Success 200 {object} map[string]string "Service is healthy"
// @Failure 500 {object} map[string]interface{} "Service or database is unhealthy"
// @Router /health [get]
func CheckHealth(c *gin.Context) {
	pool := c.MustGet("Pool").(*pgxpool.Pool)

	// slog.Info("Log level Info test", "key", "value")
	// slog.Warn("Log level Warn test", "key", "value")
	// slog.Error("Log level Error test", "key", "value")

	ctx := c.Request.Context()
	if _, err := pool.Exec(ctx, "SELECT version()"); err != nil {
		slog.Error("failed to query postgres version", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "failed to query postgres version",
			"detail": err,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"http server": "healthy",
		"database":    "healthy",
	})
}
