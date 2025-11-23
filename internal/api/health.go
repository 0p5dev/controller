package api

import (
	"net/http"

	"github.com/digizyne/lfcont/tools"
	"github.com/gin-gonic/gin"
)

func (app *App) CheckHealth(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	userClaims, err := tools.GetUserClaims(authHeader)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"claims": userClaims,
	})
	return
	ctx := c.Request.Context()
	if _, err := app.Pool.Exec(ctx, "SELECT version()"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "failed to query postgres version",
			"detail": err,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"http server": "healthy",
		"database":    "healthy",
	})
}
