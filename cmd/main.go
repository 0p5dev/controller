package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/0p5dev/controller/internal/api"
	"github.com/0p5dev/controller/internal/middleware"
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

	err := api.Initialize(router)
	if err != nil {
		slog.Error("Failed to initialize application", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:    "0.0.0.0:8080",
		Handler: router,
	}

	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Failed to run HTTP server", "error", err)
			os.Exit(1)
		}
	}()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)
	<-shutdownSignal

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Failed to shutdown HTTP server gracefully", "error", err)
	}

	middleware.CloseDatabasePool()
	slog.Info("Application shutdown complete")
}
