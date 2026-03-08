package api

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	sharedtypes "github.com/0p5dev/controller/pkg/sharedTypes"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/uuid"
)

func getImageNameFromTarballPath(tarPath string) string {
	opener := func() (io.ReadCloser, error) {
		return os.Open(tarPath)
	}

	manifest, err := tarball.LoadManifest(opener)
	if err != nil || len(manifest) == 0 || len(manifest[0].RepoTags) == 0 {
		return "image"
	}

	repoTag := manifest[0].RepoTags[0]
	repo := repoTag
	if idx := strings.LastIndex(repoTag, ":"); idx > 0 {
		repo = repoTag[:idx]
	}
	imageName := path.Base(repo)
	if imageName == "." || imageName == "/" || imageName == "" {
		return "image"
	}

	return imageName
}

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
	userClaims := c.MustGet("userClaims").(*sharedtypes.UserClaims)

	ctx := context.Background()

	// Read gzip stream from request body
	gzipStream := c.Request.Body
	contentType := c.ContentType()
	if contentType != "application/gzip" && contentType != "application/x-gzip" {
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

	tmpTar, err := os.CreateTemp("", "uploaded-image-*.tar")
	if err != nil {
		slog.Error("Failed to create temp tar file", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to prepare uploaded image for processing",
		})
		return
	}
	tmpTarPath := tmpTar.Name()
	defer os.Remove(tmpTarPath)

	if _, err := io.Copy(tmpTar, gzr); err != nil {
		tmpTar.Close()
		slog.Error("Failed to read uploaded tarball", "error", err)
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Failed to read uploaded image tarball",
		})
		return
	}

	if err := tmpTar.Close(); err != nil {
		slog.Error("Failed to close temp tar file", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to prepare image for upload",
		})
		return
	}

	img, err := tarball.ImageFromPath(tmpTarPath, nil)
	if err != nil {
		slog.Error("Failed to parse image from tarball", "error", err)
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Invalid image tarball. Ensure it is a valid docker save archive",
		})
		return
	}

	imageName := getImageNameFromTarballPath(tmpTarPath)

	// Tag image for target registry
	arRepoUrl := os.Getenv("AR_REPO_URL")
	if arRepoUrl == "" {
		slog.Error("Missing AR_REPO_URL configuration")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Server misconfiguration: AR_REPO_URL is required",
		})
		return
	}
	uuid := uuid.New().String()
	shortTag := uuid[:8]
	targetTag := fmt.Sprintf("%s/%s:%s", arRepoUrl, imageName, shortTag)

	imageRef, err := name.ParseReference(targetTag)
	if err != nil {
		slog.Error("Failed to parse source reference", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to parse source reference: %v", err),
		})
		return
	}

	// Push image to Artifact Registry using ADC for authentication
	err = remote.Write(imageRef, img, remote.WithAuthFromKeychain(google.Keychain), remote.WithContext(ctx))
	if err != nil {
		slog.Error("Image push failed", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Image push failed: %v", err),
		})
		return
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
