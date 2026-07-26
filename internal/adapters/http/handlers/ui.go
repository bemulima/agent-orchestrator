package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	uiuc "github.com/bemulima/agent-orchestrator/internal/usecase/ui"
)

type uiQueryService interface {
	Dashboard(context.Context) (domain.Dashboard, error)
	Plans(context.Context, uiuc.ListInput) (domain.PlanSummaryPage, error)
	Runs(context.Context, uiuc.ListInput) (domain.RunSummaryPage, error)
	Tasks(context.Context, uiuc.ListInput) (domain.TaskSummaryPage, error)
	Approvals(context.Context, uiuc.ListInput) (domain.ApprovalSummaryPage, error)
	Activity(context.Context, uiuc.ListInput) (domain.ActivityPage, error)
}

type UIHandler struct {
	Queries uiQueryService
}

func (h UIHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	result, err := h.Queries.Dashboard(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h UIHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	input, err := listInput(r)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Queries.Plans(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h UIHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	input, err := listInput(r)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Queries.Runs(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h UIHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	input, err := listInput(r)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Queries.Tasks(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h UIHandler) ListApprovals(w http.ResponseWriter, r *http.Request) {
	input, err := listInput(r)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Queries.Approvals(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h UIHandler) ListActivity(w http.ResponseWriter, r *http.Request) {
	input, err := listInput(r)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Queries.Activity(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h UIHandler) Events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusNotImplemented, "streaming_unsupported", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	poll := time.NewTicker(2 * time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()

	lastEventID, err := h.writeEvents(r.Context(), w, flusher, lastEventID)
	if err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			var err error
			lastEventID, err = h.writeEvents(r.Context(), w, flusher, lastEventID)
			if err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h UIHandler) writeEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, lastEventID string) (string, error) {
	page, err := h.Queries.Activity(ctx, uiuc.ListInput{Limit: 50})
	if err != nil {
		return lastEventID, err
	}
	if lastEventID == "" {
		if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
			return lastEventID, err
		}
		flusher.Flush()
		if len(page.Items) > 0 {
			return page.Items[0].ID, nil
		}
		return "", nil
	}
	end := len(page.Items)
	for index, event := range page.Items {
		if event.ID == lastEventID {
			end = index
			break
		}
	}
	for index := end - 1; index >= 0; index-- {
		event := page.Items[index]
		payload, err := json.Marshal(event)
		if err != nil {
			return lastEventID, err
		}
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s.updated\ndata: %s\n\n", event.ID, event.ResourceType, payload); err != nil {
			return lastEventID, err
		}
	}
	flusher.Flush()
	if len(page.Items) > 0 {
		return page.Items[0].ID, nil
	}
	return lastEventID, nil
}

func listInput(r *http.Request) (uiuc.ListInput, error) {
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return uiuc.ListInput{}, fmt.Errorf("limit must be between 1 and 100: %w", domain.ErrValidation)
		}
		limit = value
	}
	return uiuc.ListInput{
		Limit: limit, Cursor: r.URL.Query().Get("cursor"), Statuses: r.URL.Query()["status"],
		ProjectID: r.URL.Query().Get("project_id"), PlanID: r.URL.Query().Get("plan_id"),
	}, nil
}
