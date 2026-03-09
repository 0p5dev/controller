package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// @Summary Health check
// @Description Check the health status of the API and database connection
// @Tags health
// @Produce json
// @Success 200 {object} map[string]string "Service is healthy"
// @Failure 500 {object} map[string]interface{} "Service or database is unhealthy"
// @Router /health [get]
func (app *App) CheckHealth(c *gin.Context) {
	// slog.Info("Log level Info test", "key", "value")
	// slog.Warn("Log level Warn test", "key", "value")
	// slog.Error("Log level Error test", "key", "value")
	ctx := c.Request.Context()
	if _, err := app.Pool.Exec(ctx, "SELECT version()"); err != nil {
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
