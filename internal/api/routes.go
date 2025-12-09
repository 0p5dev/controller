package api

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/digizyne/lfcont/docs"
)

type App struct {
	Pool *pgxpool.Pool
}

func InitializeApp(router *gin.Engine, pool *pgxpool.Pool) {
	app := &App{Pool: pool}

	url := ginSwagger.URL("http://localhost:8080/swagger/doc.json")
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	apiv1 := router.Group("/api/v1")
	apiv1.GET("/health", app.CheckHealth)

	containerImages := apiv1.Group("/container-images")
	containerImages.POST("", app.pushToContainerRegistry)

	deployments := apiv1.Group("/deployments")
	deployments.GET("/:name", app.getDeploymentByName)
	deployments.GET("", app.listDeployments)
	deployments.POST("", app.createDeployment)
}
