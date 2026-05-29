package containerImages

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/0p5dev/controller/internal/sharedUtils"
	"github.com/gin-gonic/gin"
)

type GenerateSignedUrlRequestBody struct {
	ImageName string `json:"image_name" binding:"required"`
}

func GenerateSignedUrl(c *gin.Context) {
	userClaims := c.MustGet("UserClaims").(*sharedUtils.UserClaims)
	bucketName := os.Getenv("CLOUD_STORAGE_BUCKET_NAME")
	ctx := context.Background()

	var reqBody GenerateSignedUrlRequestBody
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("storage.NewClient: %v", err)})
		return
	}
	defer client.Close()

	opts := &storage.SignedURLOptions{
		Scheme:      storage.SigningSchemeV4,
		Method:      "PUT",
		Expires:     time.Now().Add(15 * time.Minute),
		ContentType: "application/gzip",
	}

	objectName := fmt.Sprintf("%s-%s.tgz", reqBody.ImageName, userClaims.UserMetadata.AppUser.Id)
	url, err := client.Bucket(bucketName).SignedURL(objectName, opts)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Bucket(%q).SignedURL: %v", bucketName, err)})
		return
	}

	c.String(http.StatusOK, url)
}
