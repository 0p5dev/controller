package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/0p5dev/controller/internal/data/models"
	"github.com/gin-gonic/gin"
)

// @Summary Stream provisioning job status
// @Description Streams provisioning status updates for a resource using Server-Sent Events (SSE). Events are emitted until status becomes succeeded or failed, or the client disconnects.
// @Tags provisioning-jobs
// @Produce text/event-stream
// @Param job_id path string true "Job ID"
// @Success 200 {string} string "SSE stream of provisioning status updates"
// @Failure 400 {object} map[string]string "job_id is required"
// @Failure 404 {object} map[string]string "provisioning job not found for job_id"
// @Failure 500 {object} map[string]string "failed to query provisioning job status"
// @Router /provisioning-jobs/{job_id}/status [get]
func (app *App) getProvisioningJobStatus(c *gin.Context) {
	jobId := c.Param("job_id")
	if jobId == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "job_id is required",
		})
		return
	}

	// Check if row in provisioning_jobs table exists for this job
	ctx := c.Request.Context()
	var exists bool
	err := app.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM provisioning_jobs WHERE id = $1 AND completed_at IS NULL)", jobId).Scan(&exists)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed to query provisioning job status",
		})
		return
	}
	if !exists {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "provisioning job either not found or already completed for " + jobId,
		})
		return
	}

	// Set SSE headers for streaming response
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	// Create a channel to receive provisioning job status updates
	statusChan := make(chan models.ProvisioningJobUpdate)
	app.Hub.registerClient(jobId, statusChan)
	defer app.Hub.unregisterClient(jobId, statusChan)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Listen for updates and send them to the client
	for {
		select {
		case statusUpdate := <-statusChan:
			statusUpdateJson, _ := json.Marshal(map[string]string{"error": "failed to parse provisioning job update"})

			if statusUpdate.Status == "succeeded" {
				serviceUrl := "URL not available"
				err := app.Pool.QueryRow(context.Background(), "SELECT url FROM deployments WHERE id = (SELECT resource_id FROM provisioning_jobs WHERE id = $1)", jobId).Scan(&serviceUrl)
				if err != nil {
					slog.Error("Failed to query service URL for completed provisioning job", "job_id", jobId, "error", err.Error())
				}
				statusUpdate.ServiceUrl = &serviceUrl
			}

			statusUpdateJson, _ = json.Marshal(statusUpdate)

			c.SSEvent("message", string(statusUpdateJson))
			c.Writer.Flush()

			if statusUpdate.Status == "succeeded" || statusUpdate.Status == "failed" {
				return
			}
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			// Optionally, send a heartbeat to keep the connection alive
			c.SSEvent("update", "provisioning job in progress...")
			c.Writer.Flush()
		}
	}
}
