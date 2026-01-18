package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// cleanupPulumiStateFiles deletes all Pulumi state files from Cloud Storage for a given deployment
func cleanupPulumiStateFiles(ctx context.Context, deploymentName string) error {
	bucketName := os.Getenv("PULUMI_STATE_BUCKET")
	projectPrefix := fmt.Sprintf("project-%s", deploymentName)

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		slog.Warn("Failed to create storage client", "error", err)
		return fmt.Errorf("failed to create storage client: %w", err)
	}
	defer storageClient.Close()

	bucket := storageClient.Bucket(bucketName)

	// Delete all objects that contain the project name in their path
	// This handles .pulumi/stacks/project-X/, .pulumi/locks/project-X/, etc.
	it := bucket.Objects(ctx, &storage.Query{})

	var objectsToDelete []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			slog.Warn("Error iterating storage objects", "error", err)
			break
		}

		// Check if the object path contains our project directory
		if len(attrs.Name) > 0 && strings.Contains(attrs.Name, projectPrefix) {
			objectsToDelete = append(objectsToDelete, attrs.Name)
		}
	}

	// Delete objects in reverse order (deepest paths first) to handle directory markers
	deleteCount := 0
	for i := len(objectsToDelete) - 1; i >= 0; i-- {
		objName := objectsToDelete[i]
		if err := bucket.Object(objName).Delete(ctx); err != nil {
			slog.Warn("Failed to delete storage object", "object", objName, "error", err)
		} else {
			deleteCount++
		}
	}

	// Also explicitly delete directory marker objects
	directoryMarkers := []string{
		fmt.Sprintf(".pulumi/stacks/%s/", projectPrefix),
		fmt.Sprintf(".pulumi/stacks/%s", projectPrefix),
	}
	for _, dirMarker := range directoryMarkers {
		if err := bucket.Object(dirMarker).Delete(ctx); err != nil {
			// This may fail if the object doesn't exist, which is fine
			slog.Debug("Directory marker not found or already deleted", "object", dirMarker, "error", err)
		} else {
			deleteCount++
		}
	}

	return nil
}
