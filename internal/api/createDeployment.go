package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/digizyne/lfcont/tools"
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

func (app *App) createDeployment(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	userClaims, err := tools.GetUserClaims(authHeader)
	if err != nil {
		log.Printf("Authentication error: %v", err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: " + err.Error(),
		})
		return
	}
	log.Printf("Authenticated user: %s", userClaims.Email)

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
		log.Printf("DB query error: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to check existing deployments: %v", err),
		})
		return
	}
	defer rows.Close()

	if rows.Next() {
		updateNeeded = true
		if err := rows.Scan(&existingDeploymentId); err != nil {
			log.Printf("DB scan error: %v", err)
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
		log.Printf("Stack creation error: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create or select stack: %v", err),
		})
		return
	}

	w := s.Workspace()
	err = w.InstallPlugin(ctx, "gcp", "v9.3.0")
	if err != nil {
		log.Printf("Plugin install error: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to install GCP plugin: %v", err),
		})
		return
	}

	s.SetConfig(ctx, "gcp:project", auto.ConfigValue{Value: os.Getenv("GCP_PROJECT_ID")})

	_, err = s.Refresh(ctx)
	if err != nil {
		log.Printf("Refresh error: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to refresh stack: %v", err),
		})
		return
	}

	stdoutStreamer := optup.ProgressStreams(os.Stdout)

	output, err := s.Up(ctx, stdoutStreamer)
	if err != nil {
		log.Printf("Deployment error: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to update stack: %v", err),
		})
		return
	}

	// Check for errors in the deployment output
	if output.Summary.ResourceChanges == nil || len(*output.Summary.ResourceChanges) == 0 {
		log.Printf("No resource changes detected - possible deployment issue")
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
		log.Printf("No resource operations performed")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Deployment completed but no resources were processed",
		})
		return
	}

	// Get the service URL from stack outputs
	outputs, err := s.Outputs(ctx)
	if err != nil {
		log.Printf("Failed to get stack outputs: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Deployment succeeded but failed to get service URL",
		})
		return
	}

	var serviceUrl string
	if urlOutput, exists := outputs["serviceUrl"]; exists {
		serviceUrl = urlOutput.Value.(string)
		log.Printf("Service deployed successfully at: %s", serviceUrl)
	} else {
		log.Printf("Warning: serviceUrl not found in stack outputs")
		serviceUrl = "URL not available"
	}

	// Log the resource changes for debugging
	log.Printf("Deployment completed successfully. Resource changes: %+v", resourceChanges)

	// Record deployment in database
	if updateNeeded {
		_, err := app.Pool.Exec(ctx, `UPDATE deployments SET container_image=$1, min_instances=$2, max_instances=$3 WHERE id=$4`, req.ContainerImage, req.MinInstances, req.MaxInstances, existingDeploymentId)
		if err != nil {
			log.Printf("DB update error: %v", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to update deployment record: %v", err),
			})
			return
		}

		log.Printf("Deployment %s updated with new container image %s", req.Name, req.ContainerImage)
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
			log.Printf("DB insert error: %v", err)
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
