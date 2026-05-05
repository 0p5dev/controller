package api

import (
	"fmt"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/0p5dev/controller/internal/middleware"
	"github.com/0p5dev/controller/internal/routes"
)

func ensureEnvVars() error {
	requiredVars := []string{
		"POSTGRES_CONNECTION_STRING",
		"SUPABASE_JWT_SECRET",
		"GCP_PROJECT_ID",
		"GCP_REGION",
		"SERVICE_ACCOUNT_EMAIL",
		"AR_REPO_URL",
		"STRIPE_API_KEY",
		"STRIPE_WEBHOOK_SIGNING_SECRET",
	}

	for _, v := range requiredVars {
		if os.Getenv(v) == "" {
			return fmt.Errorf("environment variable %s is required", v)
		}
	}

	return nil
}

func Initialize(router *gin.Engine) error {
	// Fail fast if required environment variables are missing
	if err := ensureEnvVars(); err != nil {
		return err
	}

	// Configure CORS
	corsConfig := cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}
	router.Use(cors.New(corsConfig))

	// Not using a proxy, so disable trusted proxy checking
	router.SetTrustedProxies(nil)

	// Recovery middleware by default and logging per environment
	router.Use(gin.Recovery())
	if os.Getenv("GIN_MODE") == "release" {
		router.Use(middleware.SloggerMiddleware())
	} else {
		router.Use(gin.Logger())
	}

	// Inject neccessary dependencies into the context for handlers to use
	router.Use(middleware.DatabaseMiddleware())
	router.Use(middleware.HubMiddleware())
	router.Use(middleware.StripeMiddleware())

	// Create API routes
	routes.CreateRoutes(router)

	return nil
}
