package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"cloud.google.com/go/storage"
	sharedtypes "github.com/0p5dev/controller/pkg/sharedTypes"
	"github.com/gin-gonic/gin"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"google.golang.org/api/iterator"
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
	// Capture Pulumi output to buffer
	var outputBuffer bytes.Buffer
	stdoutStreamer := optdestroy.ProgressStreams(&outputBuffer)
	_, err = s.Destroy(ctx, stdoutStreamer)
	if err != nil {
		slog.Error("Failed to destroy stack", "stack", stackName, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to destroy Cloud Run resources: %v", err),
		})
		return
	}

	// Output all Pulumi logs at once
	if outputBuffer.Len() > 0 {
		fmt.Print(outputBuffer.String())
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

	// Delete the Cloud Storage directory
	bucketName := os.Getenv("PULUMI_STATE_BUCKET")
	projectPrefix := fmt.Sprintf("project-%s", deploymentName)

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		slog.Warn("Failed to create storage client", "error", err)
		// Continue even if storage cleanup fails
	} else {
		defer storageClient.Close()

		bucket := storageClient.Bucket(bucketName)

		// Delete all objects that contain the project name in their path
		// This handles .pulumi/stacks/project-X/, .pulumi/locks/project-X/, etc.
		it := bucket.Objects(ctx, &storage.Query{})

		var objectsToDelete []string
		for {
			attrs, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				slog.Warn("Error iterating storage objects", "error", err)
				break
			}

			// Check if the object path contains our project directory
			if len(attrs.Name) > 0 && strings.Contains(attrs.Name, projectPrefix) {
				objectsToDelete = append(objectsToDelete, attrs.Name)
			}
		}

		// Delete objects in reverse order (deepest paths first) to handle directory markers
		deleteCount := 0
		for i := len(objectsToDelete) - 1; i >= 0; i-- {
			objName := objectsToDelete[i]
			slog.Info("Deleting storage object", "object", objName)
			if err := bucket.Object(objName).Delete(ctx); err != nil {
				slog.Warn("Failed to delete storage object", "object", objName, "error", err)
			} else {
				deleteCount++
			}
		}

		// Also explicitly delete directory marker objects
		directoryMarkers := []string{
			fmt.Sprintf(".pulumi/stacks/%s/", projectPrefix),
			fmt.Sprintf(".pulumi/stacks/%s", projectPrefix),
		}
		for _, dirMarker := range directoryMarkers {
			slog.Info("Attempting to delete directory marker", "object", dirMarker)
			if err := bucket.Object(dirMarker).Delete(ctx); err != nil {
				// This may fail if the object doesn't exist, which is fine
				slog.Debug("Directory marker not found or already deleted", "object", dirMarker, "error", err)
			} else {
				deleteCount++
				slog.Info("Deleted directory marker", "object", dirMarker)
			}
		}

		if deleteCount > 0 {
			slog.Info("Deleted storage objects", "bucket", bucketName, "project", projectPrefix, "count", deleteCount)
		} else {
			slog.Info("No storage objects found to delete", "bucket", bucketName, "project", projectPrefix)
		}
	}

	slog.Info("Successfully deleted deployment", "deployment", deploymentName, "user", userClaims.Email)

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Deployment '%s' deleted successfully", deploymentName),
	})
}
