package deployments

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	"github.com/0p5dev/controller/internal/models"
	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type UpdateDeploymentRequestBody struct {
	ContainerImage *string `json:"container_image,omitempty"`
	MinInstances   *int    `json:"min_instances,omitempty"`
	MaxInstances   *int    `json:"max_instances,omitempty"`
	Port           *int    `json:"port,omitempty"`
}

// @Summary Update deployment by name
// @Description Queue an update for an existing deployment. Omitted fields keep their current values.
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Deployment name"
// @Param request body api.UpdateDeploymentRequestBody true "Deployment fields to update"
// @Success 202 {object} map[string]string "Provisioning job accepted"
// @Failure 400 {object} map[string]string "Invalid request body or missing deployment name"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Deployment not found"
// @Failure 500 {object} map[string]string "Failed to queue update"
// @Router /deployments/{name} [patch]
func UpdateOneByName(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	pool := c.MustGet("Pool").(*pgxpool.Pool)

	ctx := context.Background()
	reqCtx := c.Request.Context()

	deploymentName := c.Param("name")
	if deploymentName == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "deployment name is required",
		})
		return
	}

	var reqBody UpdateDeploymentRequestBody
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	// ensure deployment exists and belongs to user, return a 404 otherwise
	var currentDeployment models.Deployment
	err := pool.QueryRow(reqCtx, "SELECT id, container_image, min_instances, max_instances, port FROM deployments WHERE name = $1 AND user_email = $2", deploymentName, userClaims.Email).Scan(
		&currentDeployment.Id,
		&currentDeployment.ContainerImage,
		&currentDeployment.MinInstances,
		&currentDeployment.MaxInstances,
		&currentDeployment.Port,
	)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "deployment " + deploymentName + " not found",
		})
		return
	}

	// Create entry in provisioning_jobs table and return job ID to client
	var jobId string
	err = pool.QueryRow(reqCtx, "INSERT INTO provisioning_jobs (resource_id, status) VALUES ($1, 'pending') RETURNING id", currentDeployment.Id).Scan(&jobId)
	if err != nil {
		slog.Error("Failed to create provisioning job", "resource_id", currentDeployment.Id, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed to create provisioning job, update canceled",
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": "Updating deployment " + deploymentName,
		"job_id":  jobId,
	})

	go func() {
		projectID := os.Getenv("GCP_PROJECT_ID")
		region := os.Getenv("GCP_REGION")

		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
		serviceFullName := fmt.Sprintf("%s/services/%s", parent, currentDeployment.Id)

		servicesClient, err := run.NewServicesClient(ctx)
		if err != nil {
			slog.Error("Failed to create Cloud Run client", "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to create Cloud Run client: "+err.Error())
			return
		}
		defer servicesClient.Close()

		// Resolve effective values: use the request value if provided, otherwise keep existing
		effectiveImage := currentDeployment.ContainerImage
		if reqBody.ContainerImage != nil {
			effectiveImage = *reqBody.ContainerImage
		}

		effectiveMin, effectiveMax := sharedUtils.ValidateMinAndMaxInstances(reqBody.MinInstances, reqBody.MaxInstances)

		effectivePort := currentDeployment.Port
		if reqBody.Port != nil {
			effectivePort = *reqBody.Port
		}

		// Build the update mask dynamically: only include paths for fields being changed
		maskPaths := []string{"traffic"}

		if reqBody.MinInstances != nil {
			maskPaths = append(maskPaths, "scaling.min_instance_count", "template.scaling.min_instance_count")
		}
		if reqBody.MaxInstances != nil {
			maskPaths = append(maskPaths, "scaling.max_instance_count", "template.scaling.max_instance_count")
		}
		if reqBody.ContainerImage != nil || reqBody.Port != nil {
			maskPaths = append(maskPaths, "template.containers")
		}
		if reqBody.Port != nil {
			maskPaths = append(maskPaths, "template.containers.ports")
		}

		if len(maskPaths) == 0 {
			slog.Info("No fields to update", "deployment", deploymentName)
			sharedUtils.SucceedProvisioningJob(ctx, pool, jobId)
			return
		}

		serviceSpec := &runpb.Service{
			Name: serviceFullName,
			Scaling: &runpb.ServiceScaling{
				MinInstanceCount: int32(effectiveMin),
				MaxInstanceCount: int32(effectiveMax),
			},
			Traffic: []*runpb.TrafficTarget{
				{
					Type:    runpb.TrafficTargetAllocationType_TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST,
					Percent: 100,
				},
			},
			Template: &runpb.RevisionTemplate{
				Scaling: &runpb.RevisionScaling{
					MinInstanceCount: int32(effectiveMin),
					MaxInstanceCount: int32(effectiveMax),
				},
				Containers: []*runpb.Container{
					{
						Image: effectiveImage,
						Ports: []*runpb.ContainerPort{
							{ContainerPort: int32(effectivePort)},
						},
					},
				},
			},
		}

		updateOperation, err := servicesClient.UpdateService(ctx, &runpb.UpdateServiceRequest{
			Service:    serviceSpec,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: maskPaths},
		})

		if err != nil {
			slog.Error("Failed to update Cloud Run service", "service", serviceFullName, "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to update Cloud Run service: "+err.Error())
			rollbackToPreviousRevision(ctx, serviceFullName, servicesClient)
			return
		}

		_, err = updateOperation.Wait(ctx)
		if err != nil {
			slog.Error("Failed waiting for Cloud Run update", "service", serviceFullName, "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed waiting for Cloud Run update: "+err.Error())
			rollbackToPreviousRevision(ctx, serviceFullName, servicesClient)
			return
		}

		_, err = pool.Exec(ctx, "UPDATE deployments SET container_image = $1, min_instances = $2, max_instances = $3, port = $4, updated_at = NOW() WHERE id = $5", effectiveImage, effectiveMin, effectiveMax, effectivePort, currentDeployment.Id)
		if err != nil {
			slog.Error("Failed to update deployment record in database", "deployment_id", currentDeployment.Id, "error", err.Error())
			sharedUtils.FailProvisioningJob(ctx, pool, jobId, "failed to update deployment record in database: "+err.Error())
			rollbackToPreviousRevision(ctx, serviceFullName, servicesClient)
			return
		}

		sharedUtils.SucceedProvisioningJob(ctx, pool, jobId)
	}()
}

func rollbackToPreviousRevision(ctx context.Context, serviceFullName string, servicesClient *run.ServicesClient) {
	revisionsClient, err := run.NewRevisionsClient(ctx)
	if err != nil {
		slog.Error("Failed to create Revisions client for rollback", "service", serviceFullName, "error", err.Error())
		return
	}
	defer revisionsClient.Close()

	iter := revisionsClient.ListRevisions(ctx, &runpb.ListRevisionsRequest{
		Parent: serviceFullName,
	})

	var revisionNames []string
	for {
		rev, err := iter.Next()
		if err != nil {
			break
		}

		shortName := rev.GetName()
		if idx := strings.LastIndex(shortName, "/"); idx >= 0 {
			shortName = shortName[idx+1:]
		}

		revisionNames = append(revisionNames, shortName)
		if len(revisionNames) >= 2 {
			break
		}
	}

	if len(revisionNames) < 2 {
		slog.Error("Not enough revisions to perform rollback", "service", serviceFullName)
		return
	}

	// revisionNames[0] is the latest; revisionNames[1] is the one to roll back to
	previousRevision := revisionNames[1]

	// Route 100% of traffic to the previous revision
	updateOperation, err := servicesClient.UpdateService(ctx, &runpb.UpdateServiceRequest{
		Service: &runpb.Service{
			Name: serviceFullName,
			Traffic: []*runpb.TrafficTarget{
				{
					Type:     runpb.TrafficTargetAllocationType_TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION,
					Revision: previousRevision,
					Percent:  100,
				},
			},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"traffic"}},
	})
	if err != nil {
		slog.Error("Failed to update service traffic for rollback", "service", serviceFullName, "error", err.Error())
		return
	}

	if _, err = updateOperation.Wait(ctx); err != nil {
		slog.Error("Failed waiting for rollback operation to complete", "service", serviceFullName, "error", err.Error())
		return
	}
}
