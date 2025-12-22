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

func Initialize(router *gin.Engine) (*pgxpool.Pool, error) {
	pool, err := data.InitializeDatabase()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	app := &App{Pool: pool}

	corsConfig := cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
	}
	router.Use(cors.New(corsConfig))

	trustedProxies := []string{"0.0.0.0/0"}
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		return nil, fmt.Errorf("failed to set trusted proxies: %w", err)
	}

	router.Use(gin.Recovery())
	if os.Getenv("APP_ENV") == "production" {
		router.Use(middleware.SloggerMiddleware())
	} else {
		router.Use(gin.Logger())
	}

	app.CreateRoutes(router)

	return pool, nil
}
