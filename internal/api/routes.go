package api

import (
	"os"

	_ "github.com/0p5dev/controller/docs"
	"github.com/0p5dev/controller/internal/middleware"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func (app *App) CreateRoutes(router *gin.Engine) {
	swaggerUrl := "http://localhost:8080/swagger/doc.json"
	if os.Getenv("GIN_MODE") == "release" {
		swaggerUrl = "https://controller.0p5.dev/swagger/doc.json"
	}

	url := ginSwagger.URL(swaggerUrl)
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	apiv1 := router.Group("/api/v1")
	apiv1.GET("/health", app.checkHealth)
	apiv1.GET("/provisioning-jobs/:job_id/status", app.getProvisioningJobStatus)

	containerImages := apiv1.Group("/container-images")
	containerImages.Use(middleware.AuthMiddleware())
	containerImages.POST("", app.pushToContainerRegistry)

	deployments := apiv1.Group("/deployments")
	deployments.Use(middleware.AuthMiddleware())
	deployments.GET("/:name", app.getDeploymentByName)
	deployments.PATCH("/:name", app.updateDeploymentByName)
	deployments.DELETE("/:name", app.deleteDeploymentByName)
	deployments.GET("", app.listDeployments)
	deployments.POST("", app.createDeployment)
}
