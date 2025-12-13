package api

import (
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/moby/moby/client"

	"github.com/digizyne/lfcont/tools"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/uuid"
)

// @Summary Push container image to registry
// @Description Upload a container image tarball and push it to Google Artifact Registry
// @Tags container-images
// @Accept application/x-gzip
// @Produce json
// @Security BearerAuth
// @Param image body string true "Gzipped container image tarball"
// @Success 200 {object} map[string]string "Image pushed successfully with FQIN"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Failed to push image"
// @Router /container-images [post]
func (app *App) pushToContainerRegistry(c *gin.Context) {
	ctx := context.Background()

	authHeader := c.GetHeader("Authorization")
	userClaims, err := tools.GetUserClaims(authHeader)
	if err != nil {
		slog.Error("Authentication error", "error", err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: " + err.Error(),
		})
		return
	}

	// Initialize Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.Error("Docker client error", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to connect to Docker daemon. Is Docker running?",
		})
		return
	}
	defer cli.Close()

	// Read gzip stream from request body
	gzipStream := c.Request.Body
	if c.ContentType() != "application/gzip" {
		c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{"error": "Content-Type must be application/gzip"})
		return
	}
	gzr, err := gzip.NewReader(gzipStream)
	if err != nil {
		slog.Error("Gzip reader error", "error", err)
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Failed to create gzip reader (invalid gzip data).",
		})
		return
	}
	defer gzr.Close()

	// Load image into Docker daemon
	imageLoadResponse, err := cli.ImageLoad(ctx, gzr, client.ImageLoadWithQuiet(true))
	if err != nil {
		slog.Error("Docker ImageLoad error", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Docker ImageLoad failed. Is the tar archive a valid 'docker save' output? Error: %v", err),
		})
		return
	}
	defer imageLoadResponse.Body.Close()

	// Get image details (specifically image ID and name so that we can tag it)
	imageDetails, err := tools.GetContainerImageDetails(imageLoadResponse)
	if err != nil {
		slog.Error("Error getting image details", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to get image details: %v", err),
		})
		return
	}
	imageID := imageDetails.ImageID
	imageName := imageDetails.ImageName

	// Tag image for target registry
	arRepoUrl := os.Getenv("AR_REPO_URL")
	uuid := uuid.New().String()
	shortTag := uuid[:8]
	targetTag := fmt.Sprintf("%s/%s:%s", arRepoUrl, imageName, shortTag)
	err = cli.ImageTag(ctx, imageID, targetTag)
	if err != nil {
		slog.Error("Docker ImageTag error", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Docker ImageTag failed: %v", err),
		})
		return
	}

	// Delete original image before tagging from local Docker daemon to free up space
	_, err = cli.ImageRemove(ctx, imageID, client.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		slog.Warn("Failed to remove original image from local Docker daemon", "image_id", imageID, "error", err)
	}

	// Get image from local Docker daemon
	imageRef, err := name.ParseReference(targetTag)
	if err != nil {
		slog.Error("Failed to parse source reference", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to parse source reference: %v", err),
		})
		return
	}
	img, err := daemon.Image(imageRef)
	if err != nil {
		slog.Error("Failed to read image from local Docker daemon", "image_ref", imageRef, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to read image from local Docker daemon. Ensure Docker is running and image '%s' exists. Error: %v", imageRef, err),
		})
		return
	}

	// Authenticate to Artifact Registry using Service Account key
	key, err := os.ReadFile("./sakey.json")
	if err != nil {
		slog.Error("Failed to read Service Account key file", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to read Service Account key file: %v", err),
		})
		return
	}
	auth := authn.FromConfig(authn.AuthConfig{
		Username: "_json_key",
		Password: string(key),
	})

	// Push image to Artifact Registry
	err = remote.Write(imageRef, img, remote.WithAuth(auth), remote.WithContext(ctx))
	if err != nil {
		slog.Error("Image push failed", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Image push failed: %v", err),
		})
		return
	}

	// Delete image from local Docker daemon to free up space
	_, err = cli.ImageRemove(ctx, targetTag, client.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		slog.Warn("Failed to remove image from local Docker daemon", "target_tag", targetTag, "error", err)
	}

	// Record pushed image in database
	_, err = app.Pool.Exec(ctx, `
			INSERT INTO container_images (fqin, user_email)
			VALUES ($1, $2)
		`, targetTag, userClaims.Email)
	if err != nil {
		slog.Error("DB insert error", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to record image in database: %v", err),
		})
		return
	}

	slog.Info("Successfully pushed image to registry", "target_tag", targetTag)
	c.JSON(http.StatusOK, gin.H{
		"fqin": targetTag,
	})
}
