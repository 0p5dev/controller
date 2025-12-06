package api

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	Pool *pgxpool.Pool
}

func InitializeApp(router *gin.Engine, pool *pgxpool.Pool) {
	app := &App{Pool: pool}

	router.GET("/health", app.CheckHealth)

	apiv1 := router.Group("/api/v1")
	apiv1.GET("/health", app.CheckHealth)

	auth := apiv1.Group("/auth")
	auth.POST("/register", app.register)
	auth.POST("/login", app.login)
	auth.GET("/supabase-credentials", app.getSupabaseCredentials)

	containerImages := apiv1.Group("/container-images")
	containerImages.POST("", app.pushToContainerRegistry)

	deployments := apiv1.Group("/deployments")
	deployments.GET("/:name", app.getDeploymentByName)
	deployments.GET("", app.listDeployments)
	deployments.POST("", app.createDeployment)
}
