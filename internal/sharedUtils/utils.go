package sharedUtils

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func HashEmail(email string) string {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	hashedEmail := sha256.Sum256([]byte(normalizedEmail))
	return hex.EncodeToString(hashedEmail[:])[:16] // Use first 16 chars of hash for uniqueness and obfuscation
}

func ValidateMinAndMaxInstances(min *int, max *int) (int, int) {
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

func SucceedProvisioningJob(ctx context.Context, pool *pgxpool.Pool, jobId string) {
	_, execErr := pool.Exec(ctx, "UPDATE provisioning_jobs SET status = 'succeeded', completed_at = NOW() WHERE id = $1", jobId)
	if execErr != nil {
		slog.Error("Failed to update provisioning job status", "job_id", jobId, "error", execErr.Error())
	}
}

func FailProvisioningJob(ctx context.Context, pool *pgxpool.Pool, jobId string, errMsg string) {
	slog.Error("Provisioning job failed", "job_id", jobId, "error", errMsg)
	_, execErr := pool.Exec(ctx, "UPDATE provisioning_jobs SET status = 'failed', completed_at = NOW() WHERE id = $1", jobId)
	if execErr != nil {
		slog.Error("Failed to update provisioning job status", "job_id", jobId, "error", execErr.Error())
	}
}
