package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/0p5dev/controller/internal/data/models"
	sharedtypes "github.com/0p5dev/controller/pkg/sharedTypes"
)

type PaginatedDeploymentsResponse struct {
	Deployments []models.Deployment `json:"deployments"`
	Count       int                 `json:"count"`
	Page        int                 `json:"page"`
	Limit       int                 `json:"limit"`
	TotalPages  int                 `json:"total_pages"`
}

// @Summary List deployments
// @Description Get a paginated list of deployments for the authenticated user
// @Tags deployments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number (default: 1)"
// @Param limit query int false "Items per page (default: 10, max: 100)"
// @Param search query string false "Search in name, url, and container_image"
// @Success 200 {object} api.PaginatedDeploymentsResponse "Paginated list of deployments"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Failed to retrieve deployments"
// @Router /deployments [get]
func (app *App) listDeployments(c *gin.Context) {
	userClaims := c.MustGet("userClaims").(*sharedtypes.UserClaims)

	ctx := c.Request.Context()

	// Parse pagination parameters
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "10")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 || limit > 100 {
		limit = 10 // Default limit with max of 100
	}

	offset := (page - 1) * limit

	// Parse search parameters
	search := c.Query("search")

	// Build dynamic WHERE clause and args
	var whereConditions []string
	var args []interface{}
	argIndex := 1

	// Always filter by authenticated user's deployments (users can only see their own)
	whereConditions = append(whereConditions, fmt.Sprintf("user_email = $%d", argIndex))
	args = append(args, userClaims.Email)
	argIndex++

	// Add search filter (searches across name, url, and container_image)
	if search != "" {
		searchPattern := "%" + strings.ToLower(search) + "%"
		whereConditions = append(whereConditions, fmt.Sprintf("(LOWER(name) LIKE $%d OR LOWER(url) LIKE $%d OR LOWER(container_image) LIKE $%d)", argIndex, argIndex, argIndex))
		args = append(args, searchPattern)
		argIndex++
	}

	whereClause := ""
	if len(whereConditions) > 0 {
		whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
	}

	// Get total count for pagination
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM deployments %s", whereClause)
	var totalCount int
	err = app.Pool.QueryRow(ctx, countQuery, args...).Scan(&totalCount)
	if err != nil {
		slog.Error("Error counting deployments", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to count deployments",
		})
		return
	}

	// Get deployments with pagination
	query := fmt.Sprintf(`
		SELECT * FROM deployments
		%s
		ORDER BY name ASC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	// Add limit and offset to args
	args = append(args, limit, offset)

	rows, err := app.Pool.Query(ctx, query, args...)
	if err != nil {
		slog.Error("Error querying deployments", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to query deployments",
		})
		return
	}
	defer rows.Close()

	var deployments []models.Deployment
	for rows.Next() {
		var deployment models.Deployment
		err := rows.Scan(
			&deployment.Id,
			&deployment.Name,
			&deployment.Url,
			&deployment.ContainerImage,
			&deployment.UserEmail,
			&deployment.MinInstances,
			&deployment.MaxInstances,
			&deployment.CreatedAt,
			&deployment.UpdatedAt,
		)
		if err != nil {
			slog.Error("Error scanning deployment row", "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to parse deployment data",
			})
			return
		}
		deployments = append(deployments, deployment)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Error iterating deployment rows", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to read deployment data",
		})
		return
	}

	// Calculate total pages
	totalPages := (totalCount + limit - 1) / limit // Ceiling division

	// Build response
	response := PaginatedDeploymentsResponse{
		Deployments: deployments,
		Count:       totalCount,
		Page:        page,
		Limit:       limit,
		TotalPages:  totalPages,
	}

	slog.Info("Retrieved deployments", "user", userClaims.Email, "count", len(deployments), "page", page, "total_pages", totalPages)

	c.JSON(http.StatusOK, response)
}
