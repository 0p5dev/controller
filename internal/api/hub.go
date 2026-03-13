package api

import (
	"sync"

	"github.com/0p5dev/controller/internal/data/models"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[string][]chan models.ProvisioningJobUpdate // map of deploymentId to list of client channels
}

func (hub *Hub) registerClient(jobId string, statusChan chan models.ProvisioningJobUpdate) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.clients[jobId] = append(hub.clients[jobId], statusChan)
}

func (hub *Hub) unregisterClient(jobId string, statusChan chan models.ProvisioningJobUpdate) {
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

func (hub *Hub) Broadcast(update models.ProvisioningJobUpdate) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if chans, exists := hub.clients[update.Id]; exists {
		for _, ch := range chans {
			ch <- update
		}
	}
}
