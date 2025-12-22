package api

import (
	_ "github.com/0p5dev/controller/docs"
	"github.com/0p5dev/controller/internal/middleware"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func (app *App) CreateRoutes(router *gin.Engine) {
	url := ginSwagger.URL("http://localhost:8080/swagger/doc.json")
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	apiv1 := router.Group("/api/v1")
	apiv1.GET("/health", app.CheckHealth)

	containerImages := apiv1.Group("/container-images")
	containerImages.Use(middleware.AuthMiddleware())
	containerImages.POST("", app.pushToContainerRegistry)

	deployments := apiv1.Group("/deployments")
	deployments.Use(middleware.AuthMiddleware())
	deployments.GET("/:name", app.getDeploymentByName)
	deployments.GET("", app.listDeployments)
	deployments.PUT("", app.createDeployment)
	deployments.DELETE("/:name", app.deleteDeploymentByName)
}
