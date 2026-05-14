package users

import (
	"log/slog"
	"net/http"

	"github.com/0p5dev/controller/internal/models"
	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func GetOne(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	pool := c.MustGet("Pool").(*pgxpool.Pool)
	slog.Debug("Handling GetOne user request", "userClaims", userClaims.UserMetadata.AppUser.Id)

	ctx := c.Request.Context()

	users, err := pool.Query(ctx, "SELECT id, email, stripe_customer_id, stripe_payment_method_id, last_billed_at, created_at, updated_at FROM users WHERE id = $1 LIMIT 1", userClaims.UserMetadata.AppUser.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve user"})
		return
	}

	defer users.Close()

	if !users.Next() {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var user models.User
	err = users.Scan(&user.Id, &user.Email, &user.StripeCustomer_Id, &user.StripePaymentMethodId, &user.LastBilledAt, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user data"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user": user})
}
