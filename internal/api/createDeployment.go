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
	if req.MinInstances < 0 {
		req.MinInstances = 0
	}
	if req.MinInstances > 10 {
		req.MinInstances = 10
	}
	if req.MaxInstances <= 0 {
		req.MaxInstances = 1
	}
	if req.MaxInstances > 10 {
		req.MaxInstances = 10
	}
	if req.MaxInstances < req.MinInstances {
		req.MaxInstances = req.MinInstances
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

func buildServiceSpec(req RequestBody, fullName string, userEmail string) *runpb.Service {
	labels := map[string]string{
		"created_by": "0p5dev_controller",
		"user":       userEmail,
	}

	serviceSpec := &runpb.Service{
		Labels: labels,
		Scaling: &runpb.ServiceScaling{
			MinInstanceCount: int32(req.MinInstances),
			MaxInstanceCount: int32(req.MaxInstances),
		},
		Template: &runpb.RevisionTemplate{
			ServiceAccount: os.Getenv("SERVICE_ACCOUNT_EMAIL"),
			Scaling: &runpb.RevisionScaling{
				MinInstanceCount: int32(req.MinInstances),
				MaxInstanceCount: int32(req.MaxInstances),
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

func updateCloudRunService(ctx context.Context, servicesClient *run.ServicesClient, serviceFullName string, req RequestBody, userEmail string) (*runpb.Service, error) {
	updateOp, err := servicesClient.UpdateService(ctx, &runpb.UpdateServiceRequest{
		Service:    buildServiceSpec(req, serviceFullName, userEmail),
		UpdateMask: serviceUpdateMask,
	})
	if err != nil {
		return nil, err
	}

	return updateOp.Wait(ctx)
}

func createCloudRunService(ctx context.Context, servicesClient *run.ServicesClient, parent, serviceID string, req RequestBody, userEmail string) (*runpb.Service, error) {
	createOp, err := servicesClient.CreateService(ctx, &runpb.CreateServiceRequest{
		Parent:    parent,
		Service:   buildServiceSpec(req, "", userEmail),
		ServiceId: serviceID,
	})
	if err != nil {
		return nil, err
	}

	return createOp.Wait(ctx)
}

func upsertCloudRunService(ctx context.Context, servicesClient *run.ServicesClient, parent, serviceFullName, serviceID string, req RequestBody, userEmail string, preferUpdate bool) (*runpb.Service, error) {
	if preferUpdate {
		service, err := updateCloudRunService(ctx, servicesClient, serviceFullName, req, userEmail)
		if status.Code(err) != codes.NotFound {
			return service, err
		}

		return createCloudRunService(ctx, servicesClient, parent, serviceID, req, userEmail)
	} else {
		service, err := createCloudRunService(ctx, servicesClient, parent, serviceID, req, userEmail)
		if status.Code(err) != codes.AlreadyExists {
			return service, err
		}
		return updateCloudRunService(ctx, servicesClient, serviceFullName, req, userEmail)
	}
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
// @Failure 408 {object} map[string]string "Deployment cancelled by client"
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

	// Send 202 to client and provision in a goroutine
	c.JSON(http.StatusAccepted, gin.H{
		"message": "Provisioning deployment",
	})

	go func() {
		applyRequestDefaults(&req)

		// Check for existing deployment with the same name
		hashedEmail := hashEmail(userClaims.Email)
		serviceId := fmt.Sprintf("%s-%s", req.Name, hashedEmail)
		updateNeeded := false

		var existingDeployment bool
		err := app.Pool.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM deployments WHERE id=$1)`, serviceId).Scan(&existingDeployment)
		if err != nil {
			slog.Error("Failed to check existing deployments", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to check existing deployments: %v", err),
			})
			return
		}

		if existingDeployment {
			updateNeeded = true
		}

		ctx := context.Background()
		reqCtx := c.Request.Context()

		projectID := os.Getenv("GCP_PROJECT_ID")
		region := os.Getenv("GCP_REGION")

		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
		serviceFullName := fmt.Sprintf("%s/services/%s", parent, serviceId)

		servicesClient, err := run.NewServicesClient(ctx)
		if err != nil {
			slog.Error("Failed to create Cloud Run client", "error", err.Error())
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to create Cloud Run client: %v", err),
			})
			return
		}
		defer servicesClient.Close()

		service, err := upsertCloudRunService(reqCtx, servicesClient, parent, serviceFullName, serviceId, req, hashedEmail, updateNeeded)

		if err != nil {
			// Check if cancellation was the cause
			if reqCtx.Err() != nil {
				slog.Warn("Deployment cancelled, initiating cleanup", "deployment", req.Name)

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

		// Ensure public access using Cloud Run service IAM policy
		if err := ensurePublicInvokerAccess(ctx, servicesClient, serviceFullName); err != nil {
			slog.Error("Failed to set IAM policy", "error", err.Error())
			// Attempt to delete the service since it's not publicly accessible and likely unusable for the user
			if cleanupErr := deleteCloudRunServiceIfExists(context.Background(), servicesClient, serviceFullName); cleanupErr != nil {
				slog.Error("Failed to cleanup after IAM policy failure. Cloud Run service may need manual cleanup", "error", cleanupErr.Error())
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Deployment failed, unable to configure public access: %v", err),
			})
			return
		}

		// Record deployment in database
		if updateNeeded {
			_, err := app.Pool.Exec(ctx, `UPDATE deployments SET container_image=$1, min_instances=$2, max_instances=$3 WHERE id=$4`, req.ContainerImage, req.MinInstances, req.MaxInstances, serviceId)
			if err != nil {
				slog.Error("Failed to update deployment record", "error", err.Error())
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": fmt.Sprintf("Failed to update deployment record: %v", err),
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"service_url": serviceUrl,
			})
			return
		} else {
			_, err = app.Pool.Exec(ctx, `
				INSERT INTO deployments (id, name, url, container_image, user_email, min_instances, max_instances)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, serviceId, req.Name, serviceUrl, req.ContainerImage, userClaims.Email, req.MinInstances, req.MaxInstances)
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
	}()
}
