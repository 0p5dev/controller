package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/0p5dev/controller/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[string][]chan models.ProvisioningJobUpdate // map of deploymentId to list of client channels
}

func (hub *Hub) RegisterClient(jobId string, statusChan chan models.ProvisioningJobUpdate) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.clients[jobId] = append(hub.clients[jobId], statusChan)
}

func (hub *Hub) UnregisterClient(jobId string, statusChan chan models.ProvisioningJobUpdate) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	chans := hub.clients[jobId]
	for i, ch := range chans {
		if ch == statusChan {
			hub.clients[jobId] = append(chans[:i], chans[i+1:]...)
			break
		}
	}
	if len(hub.clients[jobId]) == 0 {
		delete(hub.clients, jobId)
	}
	close(statusChan)
}

func (hub *Hub) broadcast(update models.ProvisioningJobUpdate) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if chans, exists := hub.clients[update.Id]; exists {
		for _, ch := range chans {
			ch <- update
		}
	}
}

func (hub *Hub) listenForProvisioningJobUpdates(onUpdate func(models.ProvisioningJobUpdate)) error {
	ctx := context.Background()
	postgresConnectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	conn, err := pgx.Connect(ctx, postgresConnectionString)
	if err != nil {
		return fmt.Errorf("error making dedicated connection to database for LISTEN/NOTIFY: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "LISTEN provisioning_jobs_updates"); err != nil {
		return fmt.Errorf("LISTEN failed: %w", err)
	}

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err // caller can restart/backoff
		}

		var update models.ProvisioningJobUpdate
		if err := json.Unmarshal([]byte(notification.Payload), &update); err != nil {
			slog.Warn("invalid provisioning_jobs notification payload", "payload", notification.Payload, "error", err)
			continue
		}

		onUpdate(update)
	}
}

func HubMiddleware() gin.HandlerFunc {
	hub := &Hub{clients: make(map[string][]chan models.ProvisioningJobUpdate)}

	go func() {
		if err := hub.listenForProvisioningJobUpdates(func(update models.ProvisioningJobUpdate) {
			slog.Info("Received provisioning job update", "update", update)
			hub.broadcast(update)
		}); err != nil {
			slog.Error("Error listening for provisioning job updates, disconnected", "error", err)
		}
	}()

	return func(c *gin.Context) {
		c.Set("Hub", hub)
		c.Next()
	}
}
