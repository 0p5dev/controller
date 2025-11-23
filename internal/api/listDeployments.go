package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/digizyne/lfcont/tools"
)

type DeploymentResponse struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	Tier           string `json:"tier"`
	ContainerImage string `json:"container_image"`
	Username       string `json:"username"`
}

type PaginatedDeploymentsResponse struct {
	Deployments []DeploymentResponse `json:"deployments"`
	Total       int                  `json:"total"`
	Page        int                  `json:"page"`
	Limit       int                  `json:"limit"`
	TotalPages  int                  `json:"total_pages"`
}

func (app *App) listDeployments(c *gin.Context) {
	// Extract user claims for authentication and filtering
	authHeader := c.GetHeader("Authorization")
	userClaims, err := tools.GetUserClaims(authHeader)
	if err != nil {
		log.Printf("Authentication error: %v", err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: " + err.Error(),
		})
		return
	}

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
	username := c.Query("username")
	tier := c.Query("tier")

	// Build dynamic WHERE clause and args
	var whereConditions []string
	var args []interface{}
	argIndex := 1

	// Always filter by authenticated user's deployments (users can only see their own)
	whereConditions = append(whereConditions, fmt.Sprintf("username = $%d", argIndex))
	args = append(args, userClaims.Email)
	argIndex++

	// Add search filter (searches across name, url, and container_image)
	if search != "" {
		searchPattern := "%" + strings.ToLower(search) + "%"
		whereConditions = append(whereConditions, fmt.Sprintf("(LOWER(name) LIKE $%d OR LOWER(url) LIKE $%d OR LOWER(container_image) LIKE $%d)", argIndex, argIndex, argIndex))
		args = append(args, searchPattern)
		argIndex++
	}

	// Add username filter (for admin use - but currently limited to own deployments)
	if username != "" && username == userClaims.Email {
		// This is redundant given our security model, but kept for API consistency
		whereConditions = append(whereConditions, fmt.Sprintf("username = $%d", argIndex))
		args = append(args, username)
		argIndex++
	}

	// Add tier filter
	if tier != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("tier = $%d", argIndex))
		args = append(args, tier)
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
		log.Printf("Error counting deployments: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to count deployments",
		})
		return
	}

	// Get deployments with pagination
	query := fmt.Sprintf(`
		SELECT name, url, tier, container_image, username 
		FROM deployments 
		%s 
		ORDER BY name ASC 
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	// Add limit and offset to args
	args = append(args, limit, offset)

	rows, err := app.Pool.Query(ctx, query, args...)
	if err != nil {
		log.Printf("Error querying deployments: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to query deployments",
		})
		return
	}
	defer rows.Close()

	var deployments []DeploymentResponse
	for rows.Next() {
		var deployment DeploymentResponse
		err := rows.Scan(
			&deployment.Name,
			&deployment.URL,
			&deployment.Tier,
			&deployment.ContainerImage,
			&deployment.Username,
		)
		if err != nil {
			log.Printf("Error scanning deployment row: %v", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to parse deployment data",
			})
			return
		}
		deployments = append(deployments, deployment)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating deployment rows: %v", err)
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
		Total:       totalCount,
		Page:        page,
		Limit:       limit,
		TotalPages:  totalPages,
	}

	log.Printf("User %s retrieved %d deployments (page %d/%d)", userClaims.Email, len(deployments), page, totalPages)

	c.JSON(http.StatusOK, response)
}
