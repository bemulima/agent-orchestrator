package health

import (
	"context"
	"sync"

	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	StatusOK       = "ok"
	StatusDegraded = "degraded"
)

type DependencyStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Result struct {
	Status       string                      `json:"status"`
	Dependencies map[string]DependencyStatus `json:"dependencies"`
}

// CheckReadiness checks all configured dependencies concurrently.
type CheckReadiness struct {
	Dependencies []repository.HealthChecker
}

// Handle returns a stable, secret-free dependency summary.
func (uc CheckReadiness) Handle(ctx context.Context) Result {
	result := Result{
		Status:       StatusOK,
		Dependencies: make(map[string]DependencyStatus, len(uc.Dependencies)),
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, dependency := range uc.Dependencies {
		dependency := dependency
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := DependencyStatus{Status: StatusOK}
			if err := dependency.Ping(ctx); err != nil {
				status = DependencyStatus{Status: StatusDegraded, Error: "unavailable"}
			}
			mu.Lock()
			result.Dependencies[dependency.Name()] = status
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, dependency := range result.Dependencies {
		if dependency.Status != StatusOK {
			result.Status = StatusDegraded
			break
		}
	}
	return result
}
