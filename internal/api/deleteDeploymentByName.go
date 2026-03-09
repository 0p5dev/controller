package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	sharedtypes "github.com/0p5dev/controller/pkg/sharedTypes"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// @Summary Delete a deployment
// @Description Delete a Cloud Run deployment and remove it from the database
// @Tags deployments
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

	projectID := os.Getenv("GCP_PROJECT_ID")
	region := os.Getenv("GCP_REGION")

	serviceFullName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, deploymentId)

	servicesClient, err := run.NewServicesClient(ctx)
	if err != nil {
		slog.Error("Failed to create Cloud Run client", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create Cloud Run client: %v", err),
		})
		return
	}
	defer servicesClient.Close()

	deleteOp, err := servicesClient.DeleteService(ctx, &runpb.DeleteServiceRequest{Name: serviceFullName})
	if err != nil {
		slog.Error("Failed to delete Cloud Run service", "service", serviceFullName, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to destroy Cloud Run resources: %v", err),
		})
		return
	}

	if _, err := deleteOp.Wait(ctx); err != nil && status.Code(err) != codes.NotFound {
		slog.Error("Failed waiting for Cloud Run deletion", "service", serviceFullName, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to destroy Cloud Run resources: %v", err),
		})
		return
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

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Deployment '%s' deleted successfully", deploymentName),
	})
}
