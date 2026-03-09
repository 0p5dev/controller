package main

import (
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/0p5dev/controller/internal/api"
)

// @title           0p5dev Controller API
// @version         1.0
// @description     A REST API for managing Cloud Run deployments and container images
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.url    http://www.swagger.io/support
// @contact.email  support@swagger.io

// @license.name  MIT
// @license.url   https://opensource.org/licenses/MIT

// @host      localhost:8080
// @BasePath  /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Bearer JWT token in the format: Bearer <token>

func main() {
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = "development"
	}
	gin.SetMode(ginMode)
	router := gin.New()
	dbConnectionPool, err := api.Initialize(router)
	if err != nil {
		slog.Error("Failed to initialize application", "error", err)
		os.Exit(1)
	}
	defer dbConnectionPool.Close()
	router.Run("0.0.0.0:8080")
}
