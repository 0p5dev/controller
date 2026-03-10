package api

import (
	"sync"

	"github.com/0p5dev/controller/internal/data/models"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[string][]chan string
}

func (hub *Hub) registerClient(deploymentId string, statusChan chan string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.clients[deploymentId] = append(hub.clients[deploymentId], statusChan)
}

func (hub *Hub) unregisterClient(deploymentId string, statusChan chan string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	chans := hub.clients[deploymentId]
	for i, ch := range chans {
		if ch == statusChan {
			hub.clients[deploymentId] = append(chans[:i], chans[i+1:]...)
			break
		}
	}
	if len(hub.clients[deploymentId]) == 0 {
		delete(hub.clients, deploymentId)
	}
	close(statusChan)
}

func (hub *Hub) Broadcast(update models.ProvisioningJobUpdate) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if chans, exists := hub.clients[update.DeploymentId]; exists {
		for _, ch := range chans {
			ch <- update.Status
		}
	}
}
