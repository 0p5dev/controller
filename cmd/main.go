package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"github.com/digizyne/lfcont/internal/api"
	"github.com/digizyne/lfcont/internal/data"
	"github.com/gin-contrib/cors"
)

// @title           OpsController API
// @version         1.0
// @description     A REST API for managing Cloud Run deployments and container images
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.url    http://www.swagger.io/support
// @contact.email  support@swagger.io

// @license.name  Apache 2.0
// @license.url   http://www.apache.org/licenses/LICENSE-2.0.html

// @host      localhost:8080
// @BasePath  /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

func main() {
	pool, err := data.InitializeDatabase()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer pool.Close()

	corsConfig := cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}

	router := gin.Default()
	router.Use(cors.New(corsConfig))
	api.InitializeApp(router, pool)
	router.Run("0.0.0.0:8080")
}
