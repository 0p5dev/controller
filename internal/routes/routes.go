package routes

import (
	"os"

	_ "github.com/0p5dev/controller/docs"
	"github.com/0p5dev/controller/internal/middleware"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	billingHandler "github.com/0p5dev/controller/internal/handlers/billing"
	containerImagesHandler "github.com/0p5dev/controller/internal/handlers/containerImages"
	deploymentsHandler "github.com/0p5dev/controller/internal/handlers/deployments"
	healthHandler "github.com/0p5dev/controller/internal/handlers/health"
	provisioningJobsHandler "github.com/0p5dev/controller/internal/handlers/provisioningJobs"
)

func CreateRoutes(router *gin.Engine) {
	swaggerUrl := "http://localhost:8080/swagger/doc.json"
	if os.Getenv("GIN_MODE") == "release" {
		swaggerUrl = "https://controller.0p5.dev/swagger/doc.json"
	}

	url := ginSwagger.URL(swaggerUrl)
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	apiv1 := router.Group("/api/v1")

	apiv1.GET("/health", healthHandler.CheckHealth)

	apiv1.GET("/provisioning-jobs/:job_id/status", provisioningJobsHandler.GetStatus)

	containerImages := apiv1.Group("/container-images")
	containerImages.Use(middleware.AuthMiddleware())
	containerImages.POST("", middleware.PaymentMethodMiddleware(), containerImagesHandler.PushToRegistry)

	deployments := apiv1.Group("/deployments")
	deployments.Use(middleware.AuthMiddleware())
	deployments.GET("/:name", deploymentsHandler.GetOne)
	deployments.PATCH("/:name", deploymentsHandler.UpdateOneByName)
	deployments.DELETE("/:name", deploymentsHandler.DeleteOneByName)
	deployments.GET("", deploymentsHandler.GetMany)
	deployments.POST("", middleware.PaymentMethodMiddleware(), deploymentsHandler.CreateOne)

	billing := apiv1.Group("/billing")
	billing.GET("/payment-method", middleware.AuthMiddleware(), billingHandler.GetUserPaymentMethod)
	billing.POST("/setup-intent", middleware.AuthMiddleware(), billingHandler.CreateSetupIntent)
	billing.POST("/webhook", billingHandler.Webhook)
}
