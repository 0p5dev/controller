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
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/cloudrunv2"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type RequestBody struct {
	Name           string `json:"name"`
	ContainerImage string `json:"container_image"`
	MinInstances   int    `json:"min_instances,omitempty"`
	MaxInstances   int    `json:"max_instances,omitempty"`
}

// @Summary Create a new deployment
// @Description Deploy a container image to Cloud Run
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body api.RequestBody true "Deployment details"
// @Success 200 {object} map[string]string "Deployment successful with service URL"
// @Failure 400 {object} map[string]string "Invalid request payload"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Deployment failed"
// @Router /deployments [post]
func (app *App) createDeployment(c *gin.Context) {
	userClaims := c.MustGet("userClaims").(*sharedtypes.UserClaims)

	var req RequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"error":   "invalid request payload",
			"message": err.Error(),
		})
		return
	}

	// Set default values if not provided
	if req.MinInstances == 0 {
		req.MinInstances = 0
	}
	if req.MaxInstances == 0 {
		req.MaxInstances = 1
	}

	// Check for existing deployment with the same name
	updateNeeded := false
	var existingDeploymentId string
	rows, err := app.Pool.Query(c.Request.Context(), `SELECT id FROM deployments WHERE name=$1 AND user_email=$2`, req.Name, userClaims.Email)
	if err != nil {
		slog.Error("Failed to check existing deployments", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to check existing deployments: %v", err),
		})
		return
	}
	defer rows.Close()

	if rows.Next() {
		updateNeeded = true
		if err := rows.Scan(&existingDeploymentId); err != nil {
			slog.Error("Failed to scan deployment ID", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to scan deployment ID: %v", err),
			})
			return
		}
	}

	createCloudRunService := func(ctx *pulumi.Context) error {
		service, err := cloudrunv2.NewService(ctx, req.Name, &cloudrunv2.ServiceArgs{
			Location:           pulumi.String("us-central1"),
			Name:               pulumi.String(req.Name),
			DeletionProtection: pulumi.Bool(false),
			Scaling: &cloudrunv2.ServiceScalingArgs{
				MinInstanceCount: pulumi.Int(req.MinInstances),
				MaxInstanceCount: pulumi.Int(req.MaxInstances),
			},
			Template: &cloudrunv2.ServiceTemplateArgs{
				Scaling: &cloudrunv2.ServiceTemplateScalingArgs{
					MinInstanceCount: pulumi.Int(req.MinInstances),
					MaxInstanceCount: pulumi.Int(req.MaxInstances),
				},
				Containers: cloudrunv2.ServiceTemplateContainerArray{
					&cloudrunv2.ServiceTemplateContainerArgs{
						Image: pulumi.String(req.ContainerImage),
					},
				},
			},
		})
		if err != nil {
			return err
		}

		// Allow public access using Cloud Run service IAM policy
		_, err = cloudrunv2.NewServiceIamBinding(ctx, "public-access", &cloudrunv2.ServiceIamBindingArgs{
			Project:  pulumi.String(os.Getenv("GCP_PROJECT_ID")),
			Location: pulumi.String(os.Getenv("GCP_REGION")),
			Name:     service.Name,
			Role:     pulumi.String("roles/run.invoker"),
			Members: pulumi.StringArray{
				pulumi.String("allUsers"),
			},
		})
		if err != nil {
			return err
		}

		// Export the service URL as a stack output
		ctx.Export("serviceUrl", service.Uri)

		return nil
	}

	ctx := context.Background()

	// Create unique stack name using hash of email to ensure each deployment creates a new service
	hash := sha256.Sum256([]byte(userClaims.Email))
	userHash := hex.EncodeToString(hash[:])[:8] // Use first 8 characters of hash

	stackName := fmt.Sprintf("stack-%s-%s", req.Name, userHash)
	projectName := fmt.Sprintf("project-%s", req.Name)

	s, err := auto.UpsertStackInlineSource(ctx, stackName, projectName, createCloudRunService)
	if err != nil {
		slog.Error("Failed to create or select stack", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create or select stack: %v", err),
		})
		return
	}

	w := s.Workspace()
	err = w.InstallPlugin(ctx, "gcp", "v9.3.0")
	if err != nil {
		slog.Error("Failed to install GCP plugin", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to install GCP plugin: %v", err),
		})
		return
	}

	s.SetConfig(ctx, "gcp:project", auto.ConfigValue{Value: os.Getenv("GCP_PROJECT_ID")})

	_, err = s.Refresh(ctx)
	if err != nil {
		slog.Error("Failed to refresh stack", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to refresh stack: %v", err),
		})
		return
	}

	stdoutStreamer := optup.ProgressStreams(os.Stdout)

	output, err := s.Up(ctx, stdoutStreamer)
	if err != nil {
		slog.Error("Failed to update stack", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to update stack: %v", err),
		})
		return
	}

	// Check for errors in the deployment output
	if output.Summary.ResourceChanges == nil || len(*output.Summary.ResourceChanges) == 0 {
		slog.Error("Deployment completed but no resources were changed", "resourceChanges", output.Summary.ResourceChanges)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Deployment completed but no resources were changed",
		})
		return
	}

	// Check deployment result
	resourceChanges := *output.Summary.ResourceChanges
	totalChanges := 0
	for _, count := range resourceChanges {
		totalChanges += count
	}

	if totalChanges == 0 {
		slog.Error("Deployment completed but no resources were processed", "totalChanges", totalChanges)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Deployment completed but no resources were processed",
		})
		return
	}

	// Get the service URL from stack outputs
	outputs, err := s.Outputs(ctx)
	if err != nil {
		slog.Error("Deployment succeeded but failed to get service URL", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Deployment succeeded but failed to get service URL. Check your 0p5.dev dashboard to retrieve the service URL.",
		})
		return
	}

	var serviceUrl string
	if urlOutput, exists := outputs["serviceUrl"]; exists {
		serviceUrl = urlOutput.Value.(string)
		slog.Info("Deployment successful", "serviceUrl", serviceUrl)
	} else {
		slog.Warn("serviceUrl not found in stack outputs", "outputs", outputs)
		serviceUrl = "URL not available"
	}

	// Log the resource changes for debugging
	slog.Info("Deployment completed successfully", "resourceChanges", resourceChanges)

	// Record deployment in database
	if updateNeeded {
		_, err := app.Pool.Exec(ctx, `UPDATE deployments SET container_image=$1, min_instances=$2, max_instances=$3 WHERE id=$4`, req.ContainerImage, req.MinInstances, req.MaxInstances, existingDeploymentId)
		if err != nil {
			slog.Error("Failed to update deployment record", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to update deployment record: %v", err),
			})
			return
		}

		slog.Info("Deployment updated successfully", "name", req.Name, "container_image", req.ContainerImage)
		c.JSON(http.StatusOK, gin.H{
			"service_url": serviceUrl,
		})
		return
	} else {
		_, err = app.Pool.Exec(ctx, `
				INSERT INTO deployments (name, url, container_image, user_email, min_instances, max_instances)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, req.Name, serviceUrl, req.ContainerImage, userClaims.Email, req.MinInstances, req.MaxInstances)
		if err != nil {
			slog.Error("Failed to record deployment in database", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to record deployment in database: %v", err),
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"service_url": serviceUrl,
	})
}
