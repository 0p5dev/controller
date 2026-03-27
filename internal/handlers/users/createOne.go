package users

import (
	"net/http"

	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func CreateOne(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	pool := c.MustGet("Pool").(*pgxpool.Pool)

	_, err := pool.Exec(c.Request.Context(), `INSERT INTO users (email) VALUES ($1) ON CONFLICT (email) DO NOTHING`, userClaims.OauthClaims.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user", "detail": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "User created successfully"})
}
