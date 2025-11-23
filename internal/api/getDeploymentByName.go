package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/digizyne/lfcont/tools"
)

type CloudRunServiceDetails struct {
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Image       string         `json:"image"`
	Status      string         `json:"status"`
	Location    string         `json:"location"`
	CreatedTime string         `json:"created_time"`
	UpdatedTime string         `json:"updated_time"`
	Scaling     ServiceScaling `json:"scaling"`
	Metrics     ServiceMetrics `json:"metrics"`
}

type ServiceScaling struct {
	MinInstances int32 `json:"min_instances"`
	MaxInstances int32 `json:"max_instances"`
}

type ServiceMetrics struct {
	RequestsPerHour [24]int `json:"requests_per_hour"`
	CPUPerHour      [24]int `json:"cpu_per_hour"`
}

func (app *App) getDeploymentByName(c *gin.Context) {
	// Extract user claims for authentication and filtering
	authHeader := c.GetHeader("Authorization")
	userClaims, err := tools.GetUserClaims(authHeader)
	if err != nil {
		log.Printf("Authentication error: %v", err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: " + err.Error(),
		})
		return
	}

	deploymentName := c.Param("name")
	if deploymentName == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "deployment name is required",
		})
		return
	}

	ctx := context.Background()
	projectID := "local-first-476300"
	location := "us-central1"

	// Verify the deployment belongs to the authenticated user
	dbCtx := c.Request.Context()
	var dbUsername string
	err = app.Pool.QueryRow(dbCtx, "SELECT username FROM deployments WHERE name = $1", deploymentName).Scan(&dbUsername)
	if err != nil {
		log.Printf("Error finding deployment %s: %v", deploymentName, err)
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "deployment not found",
		})
		return
	}

	if dbUsername != userClaims.Email {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "access denied - deployment belongs to another user",
		})
		return
	}

	// Create Cloud Run client
	runClient, err := run.NewServicesClient(ctx)
	if err != nil {
		log.Printf("Failed to create Cloud Run client: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed to initialize Cloud Run client",
		})
		return
	}
	defer runClient.Close()

	// Get Cloud Run service details
	serviceName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, location, deploymentName)

	req := &runpb.GetServiceRequest{
		Name: serviceName,
	}

	service, err := runClient.GetService(ctx, req)
	if err != nil {
		log.Printf("Failed to get service %s: %v", deploymentName, err)
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "Cloud Run service not found",
		})
		return
	}
	// fmt.Println("service: ", service)

	// Extract service details
	var containerImage string
	var minInstances, maxInstances int32

	if service.Template != nil && service.Template.Containers != nil {
		if len(service.Template.Containers) > 0 {
			containerImage = service.Template.Containers[0].Image
		}
		if service.Template.Scaling != nil {
			minInstances = service.Template.Scaling.MinInstanceCount
			maxInstances = service.Template.Scaling.MaxInstanceCount
		}
	}

	var serviceURL string
	if service.Uri != "" {
		serviceURL = service.Uri
	}

	// Get metrics from Cloud Monitoring
	metrics, err := getServiceMetrics(ctx, projectID, location, deploymentName)
	if err != nil {
		log.Printf("Failed to get metrics for %s: %v", deploymentName, err)
		// Don't fail the request, just return empty metrics
		metrics = ServiceMetrics{
			RequestsPerHour: [24]int{},
			CPUPerHour:      [24]int{},
		}
	}

	// Build response
	details := CloudRunServiceDetails{
		Name:        deploymentName,
		URL:         serviceURL,
		Image:       containerImage,
		Location:    location,
		CreatedTime: service.CreateTime.AsTime().Format(time.RFC3339),
		UpdatedTime: service.UpdateTime.AsTime().Format(time.RFC3339),
		Scaling: ServiceScaling{
			MinInstances: minInstances,
			MaxInstances: maxInstances,
		},
		Metrics: metrics,
	}

	// Determine status
	if len(service.Conditions) > 0 {
		fmt.Println("service conditions good: ", service.Conditions)
		for _, condition := range service.Conditions {
			if condition.Type == "Ready" || condition.Type == "RoutesReady" {
				if condition.State == runpb.Condition_CONDITION_SUCCEEDED {
					details.Status = "Ready"
				} else {
					details.Status = "NotReady"
				}
				break
			}
		}
	} else {
		details.Status = "Unknown"
	}

	log.Printf("User %s retrieved details for deployment %s", userClaims.Email, deploymentName)

	c.JSON(http.StatusOK, details)
}

func getServiceMetrics(ctx context.Context, projectID, location, serviceName string) (ServiceMetrics, error) {
	// Create monitoring client
	monitoringClient, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		return ServiceMetrics{}, fmt.Errorf("failed to create monitoring client: %w", err)
	}
	defer monitoringClient.Close()

	// Set time range for metrics (last 24 hours)
	now := time.Now()
	endTime := timestamppb.New(now)
	startTime := timestamppb.New(now.Add(-24 * time.Hour))

	// Get request count metrics with 1-hour alignment
	requestsPerHour, err := getHourlyRequests(ctx, monitoringClient, projectID, location, serviceName, startTime, endTime)
	if err != nil {
		log.Printf("Failed to get hourly request metrics: %v", err)
		requestsPerHour = [24]int{}
	}

	// Get CPU utilization metrics with 1-hour alignment
	cpuPerHour, err := getHourlyCPU(ctx, monitoringClient, projectID, location, serviceName, startTime, endTime)
	if err != nil {
		log.Printf("Failed to get hourly CPU metrics: %v", err)
		cpuPerHour = [24]int{}
	}

	return ServiceMetrics{
		RequestsPerHour: requestsPerHour,
		CPUPerHour:      cpuPerHour,
	}, nil
}

func getHourlyRequests(ctx context.Context, client *monitoring.MetricClient, projectID, location, serviceName string, startTime, endTime *timestamppb.Timestamp) ([24]int, error) {
	req := &monitoringpb.ListTimeSeriesRequest{
		Name:   fmt.Sprintf("projects/%s", projectID),
		Filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND resource.labels.location="%s" AND metric.type="run.googleapis.com/request_count"`, serviceName, location),
		Interval: &monitoringpb.TimeInterval{
			EndTime:   endTime,
			StartTime: startTime,
		},
		Aggregation: &monitoringpb.Aggregation{
			AlignmentPeriod:    durationpb.New(3600 * time.Second), // 1 hour
			PerSeriesAligner:   monitoringpb.Aggregation_ALIGN_RATE,
			CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_SUM,
		},
		View: monitoringpb.ListTimeSeriesRequest_FULL,
	}

	// Initialize array with 24 zeros (one for each hour)
	var hourlyRequests [24]int

	// Create a map to store data points by hour index
	dataPoints := make(map[int]int64)

	iterator := client.ListTimeSeries(ctx, req)

	for {
		resp, err := iterator.Next()
		if err != nil {
			break
		}

		for _, point := range resp.Points {
			// Calculate which hour this data point represents (0-23, where 0 is the most recent hour)
			pointTime := point.Interval.EndTime.AsTime()
			hoursAgo := int(time.Since(pointTime).Hours())
			if hoursAgo >= 0 && hoursAgo < 24 {
				// Store in reverse order (index 0 = most recent hour, index 23 = 24 hours ago)
				dataPoints[hoursAgo] += point.Value.GetInt64Value()
			}
		}
	}

	// Fill the array with data points
	for i := 0; i < 24; i++ {
		if value, exists := dataPoints[i]; exists {
			hourlyRequests[i] = int(value)
		}
		// hourlyRequests[i] += int(i)
		// If no data exists for that hour, it remains 0
	}

	return hourlyRequests, nil
}

func getHourlyCPU(ctx context.Context, client *monitoring.MetricClient, projectID, location, serviceName string, startTime, endTime *timestamppb.Timestamp) ([24]int, error) {
	req := &monitoringpb.ListTimeSeriesRequest{
		Name:   fmt.Sprintf("projects/%s", projectID),
		Filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND resource.labels.location="%s" AND metric.type="run.googleapis.com/container/cpu/utilizations"`, serviceName, location),
		Interval: &monitoringpb.TimeInterval{
			EndTime:   endTime,
			StartTime: startTime,
		},
		Aggregation: &monitoringpb.Aggregation{
			AlignmentPeriod:    durationpb.New(3600 * time.Second), // 1 hour
			PerSeriesAligner:   monitoringpb.Aggregation_ALIGN_MEAN,
			CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_MEAN,
		},
		View: monitoringpb.ListTimeSeriesRequest_FULL,
	}

	// Initialize array with 24 zeros (one for each hour)
	var hourlyCPU [24]int

	// Create a map to store data points by hour index
	dataPoints := make(map[int]float64)

	iterator := client.ListTimeSeries(ctx, req)

	for {
		resp, err := iterator.Next()
		if err != nil {
			break
		}

		for _, point := range resp.Points {
			// Calculate which hour this data point represents (0-23, where 0 is the most recent hour)
			pointTime := point.Interval.EndTime.AsTime()
			hoursAgo := int(time.Since(pointTime).Hours())
			if hoursAgo >= 0 && hoursAgo < 24 {
				// Store CPU utilization as percentage (0-100)
				cpuPercent := point.Value.GetDoubleValue() * 100
				dataPoints[hoursAgo] = cpuPercent
			}
		}
	}

	// Fill the array with data points
	for i := 0; i < 24; i++ {
		if value, exists := dataPoints[i]; exists {
			hourlyCPU[i] = int(value)
		}
		// If no data exists for that hour, it remains 0
	}

	return hourlyCPU, nil
}
