package api

import (
	"fmt"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0p5dev/controller/internal/data"
	"github.com/0p5dev/controller/internal/middleware"
)

type App struct {
	Pool *pgxpool.Pool
}

func ensureEnvVars() error {
	requiredVars := []string{
		"POSTGRES_CONNECTION_STRING",
		"SUPABASE_JWT_SECRET",
		"GCP_PROJECT_ID",
		"GCP_REGION",
		"SERVICE_ACCOUNT_EMAIL",
		"AR_REPO_URL",
	}

	for _, v := range requiredVars {
		if os.Getenv(v) == "" {
			return fmt.Errorf("environment variable %s is required", v)
		}
	}

	return nil
}

func Initialize(router *gin.Engine) (*pgxpool.Pool, error) {
	if err := ensureEnvVars(); err != nil {
		return nil, err
	}

	corsConfig := cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}
	router.Use(cors.New(corsConfig))

	router.SetTrustedProxies(nil)

	router.Use(gin.Recovery())
	if os.Getenv("GIN_MODE") == "release" {
		router.Use(middleware.SloggerMiddleware())
	} else {
		router.Use(gin.Logger())
	}

	pool, err := data.InitializeDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	app := &App{Pool: pool}

	app.CreateRoutes(router)

	return pool, nil
}
