package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"github.com/digizyne/lfcont/internal/api"
	"github.com/digizyne/lfcont/internal/data"
	"github.com/gin-contrib/cors"
)

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
