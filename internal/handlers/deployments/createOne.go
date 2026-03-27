package deployments

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
	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CreateOneRequestBody struct {
	Name           string `json:"name"`
	ContainerImage string `json:"container_image"`
	MinInstances   *int   `json:"min_instances,omitempty,string"`
	MaxInstances   *int   `json:"max_instances,omitempty,string"`
	Port           *int   `json:"port,omitempty,string"`
}

// @Summary Create a new deployment
// @Description Queue creation of a deployment in Cloud Run and return a provisioning job ID
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body api.RequestBody true "Deployment details"
// @Success 202 {object} map[string]string "Provisioning job accepted"
// @Failure 400 {object} map[string]string "Invalid request payload"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 409 {object} map[string]string "Deployment already exists"
// @Failure 500 {object} map[string]string "Failed to queue deployment"
// @Router /deployments [post]
func CreateOne(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	pool := c.MustGet("Pool").(*pgxpool.Pool)

	ctx := context.Background()
	reqCtx := c.Request.Context()

	var reqBody CreateOneRequestBody
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request payload",
			"message": err.Error(),
		})
		return
	}

	var existingDeployment bool
	err := pool.QueryRow(reqCtx, `SELECT EXISTS(SELECT 1 FROM deployments WHERE name=$1 AND user_email=$2)`, reqBody.Name, userClaims.Email).Scan(&existingDeployment)
	if err != nil {
		slog.Error("Failed to check existing deployments", "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to check existing deployments",
			"message": err.Error(),
		})
		return
	}

	if existingDeployment {
		c.JSON(http.StatusConflict, gin.H{
			"error": "deployment " + reqBody.Name + " already exists",
		})
		return
	}

	hashedEmail := sharedUtils.HashEmail(userClaims.Email)
	serviceId := fmt.Sprintf("%s-%s", reqBody.Name, hashedEmail)

	// Create entry in provisioning_jobs table and return job ID to client
	var jobId string
	err = pool.QueryRow(reqCtx, "INSERT INTO provisioning_jobs (resource_id, status) VALUES ($1, 'pending') RETURNING id", serviceId).Scan(&jobId)
	if err != nil {
		slog.Error("Failed to create provisioning job", "resource_id", serviceId, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed to create provisioning job, update canceled",
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": "Provisioning deployment " + reqBody.Name,
		"job_id":  jobId,
	})

	go func() {
		projectID := os.Getenv("GCP_PROJECT_ID")
		region := os.Getenv("GCP_REGION")

		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
		serviceFullName := fmt.Sprintf("%s/services/%s", parent, serviceId)

		servicesClient, err := run.NewServicesClient(ctx)
		if err != nil {
			slog.Error("Failed to create Cloud Run client", "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to create Cloud Run client: "+err.Error())
			return
		}
		defer servicesClient.Close()

		effectiveMin, effectiveMax := sharedUtils.ValidateMinAndMaxInstances(reqBody.MinInstances, reqBody.MaxInstances)

		effectivePort := 8080
		if reqBody.Port != nil {
			effectivePort = *reqBody.Port
		}

		serviceSpec := &runpb.Service{
			Labels: map[string]string{
				"created_by": "0p5dev_controller",
				"user":       hashedEmail,
			},
			Scaling: &runpb.ServiceScaling{
				MinInstanceCount: int32(effectiveMin),
				MaxInstanceCount: int32(effectiveMax),
			},
			Template: &runpb.RevisionTemplate{
				ServiceAccount: os.Getenv("SERVICE_ACCOUNT_EMAIL"),
				Scaling: &runpb.RevisionScaling{
					MinInstanceCount: int32(effectiveMin),
					MaxInstanceCount: int32(effectiveMax),
				},
				Containers: []*runpb.Container{
					{
						Image: reqBody.ContainerImage,
						Ports: []*runpb.ContainerPort{
							{ContainerPort: int32(effectivePort)},
						},
					},
				},
			},
		}

		createOp, err := servicesClient.CreateService(ctx, &runpb.CreateServiceRequest{
			Parent:    parent,
			Service:   serviceSpec,
			ServiceId: serviceId,
		})
		if err != nil {
			slog.Error("Failed to create Cloud Run service", "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to construct Cloud Run service: "+err.Error())
			deleteCloudRunServiceIfExists(ctx, servicesClient, serviceFullName)
			return
		}

		service, err := createOp.Wait(ctx)
		if err != nil {
			slog.Error("Cloud Run service creation failed", "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "Cloud Run service creation failed: "+err.Error())
			deleteCloudRunServiceIfExists(ctx, servicesClient, serviceFullName)
			return
		}

		var serviceUrl string
		if service != nil && service.Uri != "" {
			serviceUrl = service.Uri
		} else {
			slog.Warn("serviceUrl not found in Cloud Run response", "deployment", reqBody.Name)
			serviceUrl = "URL not available"
		}

		// Ensure public access using Cloud Run service IAM policy
		if err := ensurePublicInvokerAccess(ctx, servicesClient, serviceFullName); err != nil {
			slog.Error("Failed to set IAM policy", "error", err.Error())
			// Attempt to delete the service since it's not publicly accessible and likely unusable for the user
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to set IAM policy for public access: "+err.Error())
			deleteCloudRunServiceIfExists(ctx, servicesClient, serviceFullName)
			return
		}

		// Record deployment in database
		_, err = pool.Exec(ctx, `
				INSERT INTO deployments (id, name, url, container_image, user_email, min_instances, max_instances)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, serviceId, reqBody.Name, serviceUrl, reqBody.ContainerImage, userClaims.Email, effectiveMin, effectiveMax)
		if err != nil {
			slog.Error("Failed to record deployment in database", "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to record deployment in database: "+err.Error())
			deleteCloudRunServiceIfExists(ctx, servicesClient, serviceFullName)
			return
		}

		sharedUtils.SucceedProvisioningJob(ctx, pool, jobId)
	}()
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

func deleteCloudRunServiceIfExists(ctx context.Context, servicesClient *run.ServicesClient, serviceFullName string) {
	deleteOp, err := servicesClient.DeleteService(ctx, &runpb.DeleteServiceRequest{Name: serviceFullName})
	if err != nil {
		slog.Error("Failed to initiate Cloud Run service deletion during cleanup", "service", serviceFullName, "error", err.Error())
		return
	}

	_, err = deleteOp.Wait(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		slog.Error("Failed to wait for Cloud Run service deletion during cleanup", "service", serviceFullName, "error", err.Error())
		return
	}
}
