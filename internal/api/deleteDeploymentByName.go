package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	sharedtypes "github.com/digizyne/lfcont/pkg/sharedTypes"
	"github.com/gin-gonic/gin"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// @Summary Delete a deployment
// @Description Delete a Cloud Run deployment and remove it from the database
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Deployment name"
// @Success 200 {object} map[string]string "Deployment deleted successfully"
// @Failure 400 {object} map[string]string "Deployment name is required"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Deployment not found"
// @Failure 500 {object} map[string]string "Failed to delete deployment"
// @Router /deployments/{name} [delete]
func (app *App) deleteDeploymentByName(c *gin.Context) {

	userClaims := c.MustGet("userClaims").(*sharedtypes.UserClaims)

	deploymentName := c.Param("name")
	if deploymentName == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "deployment name is required",
		})
		return
	}

	slog.Info("Received request to delete deployment", "deployment", deploymentName, "user", userClaims.Email)

	ctx := context.Background()

	// Verify the deployment belongs to the authenticated user
	var deploymentId string
	err := app.Pool.QueryRow(ctx, "SELECT id FROM deployments WHERE name = $1 AND user_email = $2", deploymentName, userClaims.Email).Scan(&deploymentId)
	if err != nil {
		slog.Error("Error finding deployment", "deployment", deploymentName, "user", userClaims.Email, "error", err)
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "deployment not found",
		})
		return
	}

	// Create unique stack name using hash of email (same as in createDeployment)
	hash := sha256.Sum256([]byte(userClaims.Email))
	userHash := hex.EncodeToString(hash[:])[:8]

	stackName := fmt.Sprintf("stack-%s-%s", deploymentName, userHash)
	projectName := fmt.Sprintf("project-%s", deploymentName)

	// Get the existing stack
	s, err := auto.SelectStackInlineSource(ctx, stackName, projectName, func(ctx *pulumi.Context) error {
		// Empty program since we're just destroying
		return nil
	})
	if err != nil {
		slog.Error("Failed to select stack", "stack", stackName, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to select stack: %v", err),
		})
		return
	}

	// Install GCP plugin
	w := s.Workspace()
	err = w.InstallPlugin(ctx, "gcp", "v9.3.0")
	if err != nil {
		slog.Error("Failed to install GCP plugin", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to install GCP plugin: %v", err),
		})
		return
	}

	// Set GCP project configuration
	s.SetConfig(ctx, "gcp:project", auto.ConfigValue{Value: os.Getenv("GCP_PROJECT_ID")})

	// Refresh stack state
	_, err = s.Refresh(ctx)
	if err != nil {
		slog.Warn("Failed to refresh stack", "stack", stackName, "error", err)
		// Continue with destroy even if refresh fails
	}

	// Destroy the stack (removes all Cloud Run resources)
	stdoutStreamer := optdestroy.ProgressStreams(os.Stdout)
	_, err = s.Destroy(ctx, stdoutStreamer)
	if err != nil {
		slog.Error("Failed to destroy stack", "stack", stackName, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to destroy Cloud Run resources: %v", err),
		})
		return
	}

	slog.Info("Successfully destroyed stack", "stack", stackName)

	// Remove the stack
	err = w.RemoveStack(ctx, stackName)
	if err != nil {
		slog.Warn("Failed to remove stack", "stack", stackName, "error", err)
		// Continue even if stack removal fails
	}

	// Delete the deployment from the database
	_, err = app.Pool.Exec(ctx, "DELETE FROM deployments WHERE id = $1", deploymentId)
	if err != nil {
		slog.Error("Failed to delete deployment from database", "deployment_id", deploymentId, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Cloud Run resources destroyed but failed to delete database record: %v", err),
		})
		return
	}

	slog.Info("Successfully deleted deployment", "deployment", deploymentName, "user", userClaims.Email)

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Deployment '%s' deleted successfully", deploymentName),
	})
}
