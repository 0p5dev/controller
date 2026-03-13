package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
)

func hashEmail(email string) string {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	hashedEmail := sha256.Sum256([]byte(normalizedEmail))
	return hex.EncodeToString(hashedEmail[:])[:16] // Use first 16 chars of hash for uniqueness and obfuscation
}

func validateMinAndMaxInstances(min *int, max *int) (int, int) {
	effectiveMin := 0
	effectiveMax := 1

	if min != nil {
		effectiveMin = *min
	}
	if effectiveMin < 0 {
		effectiveMin = 0
	}
	if effectiveMin > 10 {
		effectiveMin = 10
	}
	if max != nil {
		effectiveMax = *max
	}
	if effectiveMax < effectiveMin {
		effectiveMax = effectiveMin
	}
	if effectiveMax > 10 {
		effectiveMax = 10
	}

	return effectiveMin, effectiveMax
}

func (app *App) succeedProvisioningJob(ctx context.Context, jobId string) {
	_, execErr := app.Pool.Exec(ctx, "UPDATE provisioning_jobs SET status = 'succeeded', completed_at = NOW() WHERE id = $1", jobId)
	if execErr != nil {
		slog.Error("Failed to update provisioning job status", "job_id", jobId, "error", execErr.Error())
	}
}

func (app *App) failProvisioningJob(ctx context.Context, jobId string, errMsg string) {
	slog.Error("Provisioning job failed", "job_id", jobId, "error", errMsg)
	_, execErr := app.Pool.Exec(ctx, "UPDATE provisioning_jobs SET status = 'failed', completed_at = NOW() WHERE id = $1", jobId)
	if execErr != nil {
		slog.Error("Failed to update provisioning job status", "job_id", jobId, "error", execErr.Error())
	}
}
