package models

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ProvisioningJob struct {
	Id          string    `json:"id"`
	ResourceId  string    `json:"resource_id"`
	Status      string    `json:"status"` // pending | succeeded | failed
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type ProvisioningJobUpdate struct {
	Id          string  `json:"id"`
	ResourceId  string  `json:"resource_id"`
	Status      string  `json:"status"` // pending | succeeded | failed
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at"`
	ServiceUrl  *string `json:"service_url"`
}

func MigrateProvisioningJobTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS provisioning_jobs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			resource_id TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			completed_at TIMESTAMPTZ
		);

		-- Ensure provisioning_jobs table exists before creating the trigger function

		CREATE OR REPLACE FUNCTION notify_provisioning_job_update()
		RETURNS trigger AS $$
		DECLARE
		  payload json;
		BEGIN
		  -- only emit when relevant values actually changed
		  IF (OLD.status IS DISTINCT FROM NEW.status)
			 OR (OLD.completed_at IS DISTINCT FROM NEW.completed_at) THEN
		
			payload := json_build_object(
			  'id', NEW.id,
			  'resource_id', NEW.resource_id,
			  'status', NEW.status,
			  'completed_at', NEW.completed_at,
			  'created_at', NEW.created_at
			);
		
			PERFORM pg_notify('provisioning_jobs_updates', payload::text);
		  END IF;
		
		  RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		
		DROP TRIGGER IF EXISTS trg_notify_provisioning_job_update ON provisioning_jobs;
		
		CREATE TRIGGER trg_notify_provisioning_job_update
		AFTER UPDATE OF status, completed_at ON provisioning_jobs
		FOR EACH ROW
		EXECUTE FUNCTION notify_provisioning_job_update();
	`)

	return err
}
