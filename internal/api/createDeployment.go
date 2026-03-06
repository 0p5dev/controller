package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"

	iampb "cloud.google.com/go/iam/apiv1/iampb"
	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	sharedtypes "github.com/0p5dev/controller/pkg/sharedTypes"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type RequestBody struct {
	Name           string `json:"name"`
	ContainerImage string `json:"container_image"`
	MinInstances   int    `json:"min_instances,omitempty"`
	MaxInstances   int    `json:"max_instances,omitempty"`
	Port           int    `json:"port,omitempty"`
}

func applyRequestDefaults(req *RequestBody) {
	if req.MaxInstances <= 0 {
		req.MaxInstances = 1
	}
	if req.Port == 0 {
		req.Port = 8080
	}
}

var serviceUpdateMask = &fieldmaskpb.FieldMask{Paths: []string{
	"scaling.min_instance_count",
	"scaling.max_instance_count",
	"template.scaling.min_instance_count",
	"template.scaling.max_instance_count",
	"template.containers",
}}

func buildServiceSpec(req RequestBody, fullName string) *runpb.Service {
	maxInstances := req.MaxInstances
	if maxInstances <= 0 {
		maxInstances = 1
	}

	serviceSpec := &runpb.Service{
		Scaling: &runpb.ServiceScaling{
			MinInstanceCount: int32(req.MinInstances),
			MaxInstanceCount: int32(maxInstances),
		},
		Template: &runpb.RevisionTemplate{
			Scaling: &runpb.RevisionScaling{
				MinInstanceCount: int32(req.MinInstances),
				MaxInstanceCount: int32(maxInstances),
			},
			Containers: []*runpb.Container{
				{
					Image: req.ContainerImage,
					Ports: []*runpb.ContainerPort{
						{ContainerPort: int32(req.Port)},
					},
				},
			},
		},
	}

	if fullName != "" {
		serviceSpec.Name = fullName
	}

	return serviceSpec
}

func updateCloudRunService(ctx context.Context, servicesClient *run.ServicesClient, serviceFullName string, req RequestBody) (*runpb.Service, error) {
	updateOp, err := servicesClient.UpdateService(ctx, &runpb.UpdateServiceRequest{
		Service:    buildServiceSpec(req, serviceFullName),
		UpdateMask: serviceUpdateMask,
	})
	if err != nil {
		return nil, err
	}

	return updateOp.Wait(ctx)
}

func createCloudRunService(ctx context.Context, servicesClient *run.ServicesClient, parent, serviceID string, req RequestBody) (*runpb.Service, error) {
	createOp, err := servicesClient.CreateService(ctx, &runpb.CreateServiceRequest{
		Parent:    parent,
		Service:   buildServiceSpec(req, ""),
		ServiceId: serviceID,
	})
	if err != nil {
		return nil, err
	}

	return createOp.Wait(ctx)
}

func upsertCloudRunService(ctx context.Context, servicesClient *run.ServicesClient, parent, serviceFullName, serviceID string, req RequestBody, preferUpdate bool) (*runpb.Service, error) {
	if preferUpdate {
		service, err := updateCloudRunService(ctx, servicesClient, serviceFullName, req)
		if status.Code(err) != codes.NotFound {
			return service, err
		}

		return createCloudRunService(ctx, servicesClient, parent, serviceID, req)
	}

	service, err := createCloudRunService(ctx, servicesClient, parent, serviceID, req)
	if status.Code(err) != codes.AlreadyExists {
		return service, err
	}

	return updateCloudRunService(ctx, servicesClient, serviceFullName, req)
}

func deleteCloudRunServiceIfExists(ctx context.Context, servicesClient *run.ServicesClient, serviceFullName string) error {
	deleteOp, err := servicesClient.DeleteService(ctx, &runpb.DeleteServiceRequest{Name: serviceFullName})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return err
	}

	_, err = deleteOp.Wait(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}

	return nil
}

func ensurePublicInvokerAccess(ctx context.Context, servicesClient *run.ServicesClient, serviceFullName string) error {
	policy, err := servicesClient.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{Resource: serviceFullName})
	if err != nil {
		return err
	}

	for _, binding := range policy.Bindings {
		if binding.Role != "roles/run.invoker" {
			continue
		}

		if slices.Contains(binding.Members, "allUsers") {
			return nil
		}

		binding.Members = append(binding.Members, "allUsers")
		_, err = servicesClient.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{Resource: serviceFullName, Policy: policy})
		return err
	}

	policy.Bindings = append(policy.Bindings, &iampb.Binding{
		Role:    "roles/run.invoker",
		Members: []string{"allUsers"},
	})

	_, err = servicesClient.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{Resource: serviceFullName, Policy: policy})
	return err
}

func getServiceMaxInstances(service *runpb.Service) int32 {
	if service == nil || service.Template == nil || service.Template.Scaling == nil {
		return 0
	}

	return service.Template.Scaling.MaxInstanceCount
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
// @Router /deployments [put]
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

	slog.Info("Received request to create deployment", "deployment", req.Name, "user", userClaims.Email)

	applyRequestDefaults(&req)

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

	ctx := context.Background()
	reqCtx := c.Request.Context()

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		slog.Error("Missing GCP project configuration")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Server misconfiguration: GCP_PROJECT_ID is required",
		})
		return
	}

	region := os.Getenv("GCP_REGION")
	if region == "" {
		region = "us-central1"
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	serviceFullName := fmt.Sprintf("%s/services/%s", parent, req.Name)

	servicesClient, err := run.NewServicesClient(ctx)
	if err != nil {
		slog.Error("Failed to create Cloud Run client", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create Cloud Run client: %v", err),
		})
		return
	}
	defer servicesClient.Close()

	service, err := upsertCloudRunService(reqCtx, servicesClient, parent, serviceFullName, req.Name, req, updateNeeded)

	if err != nil {
		// Check if cancellation was the cause
		if reqCtx.Err() != nil {
			slog.Info("Deployment cancelled, initiating cleanup", "deployment", req.Name)

			// Use fresh context for cleanup operations
			cleanupCtx := context.Background()

			// Attempt to delete partially created service
			if cleanupErr := deleteCloudRunServiceIfExists(cleanupCtx, servicesClient, serviceFullName); cleanupErr != nil {
				slog.Error("Failed to cleanup after cancellation", "error", cleanupErr.Error())
				c.AbortWithStatusJSON(http.StatusRequestTimeout, gin.H{
					"error": "Deployment cancelled by client. Some resources may need manual cleanup.",
				})
				return
			}

			slog.Info("Successfully cleaned up resources after cancellation", "deployment", req.Name)

			c.AbortWithStatusJSON(http.StatusRequestTimeout, gin.H{
				"error": "Deployment cancelled by client",
			})
			return
		}
		slog.Error("Failed to deploy Cloud Run service", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to deploy Cloud Run service: %v", err),
		})
		return
	}

	var serviceUrl string
	if service != nil && service.Uri != "" {
		serviceUrl = service.Uri
	} else {
		slog.Warn("serviceUrl not found in Cloud Run response", "deployment", req.Name)
		serviceUrl = "URL not available"
	}

	expectedMaxInstances := int32(req.MaxInstances)
	if getServiceMaxInstances(service) != expectedMaxInstances {
		slog.Info("Enforcing Cloud Run max instances", "deployment", req.Name, "expected", expectedMaxInstances, "actual", getServiceMaxInstances(service))
		enforcedService, enforceErr := updateCloudRunService(ctx, servicesClient, serviceFullName, req)
		if enforceErr != nil {
			slog.Error("Failed to enforce max instances", "error", enforceErr.Error())
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Deployment succeeded but failed to enforce max instances: %v", enforceErr),
			})
			return
		}
		service = enforcedService
		if getServiceMaxInstances(service) != expectedMaxInstances {
			slog.Error("Max instances mismatch after enforcement", "deployment", req.Name, "expected", expectedMaxInstances, "actual", getServiceMaxInstances(service))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "Deployment succeeded but max instances did not apply as expected",
			})
			return
		}
	}

	// Ensure public access using Cloud Run service IAM policy
	if err := ensurePublicInvokerAccess(ctx, servicesClient, serviceFullName); err != nil {
		slog.Error("Failed to set IAM policy", "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Deployment succeeded but failed to configure public access: %v", err),
		})
		return
	}

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
