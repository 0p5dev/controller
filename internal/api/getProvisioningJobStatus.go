package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// @Summary Stream provisioning job status
// @Description Streams provisioning status updates for a deployment using Server-Sent Events (SSE). Events are emitted until status becomes succeeded or failed, or the client disconnects.
// @Tags provisioning-jobs
// @Produce text/event-stream
// @Param deployment_id path string true "Deployment ID"
// @Success 200 {string} string "SSE stream of provisioning status updates"
// @Failure 400 {object} map[string]string "deployment_id is required"
// @Failure 404 {object} map[string]string "provisioning job not found for deployment_id"
// @Failure 500 {object} map[string]string "failed to query provisioning job status"
// @Router /provisioning-jobs/{deployment_id}/status [get]
func (app *App) getProvisioningJobStatus(c *gin.Context) {
	deploymentId := c.Param("deployment_id")
	if deploymentId == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "deployment_id is required",
		})
		return
	}

	// Check if row in provisioning_jobs table exists for this deployment
	ctx := c.Request.Context()
	var exists bool
	err := app.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM provisioning_jobs WHERE deployment_id = $1)", deploymentId).Scan(&exists)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed to query provisioning job status",
		})
		return
	}
	if !exists {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "provisioning job not found for " + deploymentId,
		})
		return
	}

	// Set SSE headers for streaming response
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	// Create a channel to receive provisioning job status updates
	statusChan := make(chan string)
	app.Hub.registerClient(deploymentId, statusChan)
	defer app.Hub.unregisterClient(deploymentId, statusChan)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Listen for updates and send them to the client
	for {
		select {
		case statusUpdate := <-statusChan:
			c.SSEvent("message", statusUpdate)
			c.Writer.Flush()
			if statusUpdate == "succeeded" || statusUpdate == "failed" {
				return
			}
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			// Optionally, send a heartbeat to keep the connection alive
			c.SSEvent("update", "pending")
			c.Writer.Flush()
		}
	}
}
