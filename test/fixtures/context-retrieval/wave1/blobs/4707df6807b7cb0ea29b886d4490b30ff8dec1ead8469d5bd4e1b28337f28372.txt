// Package nats handles User service messages exchanged over NATS.
package nats

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/port"
	service "github.com/example/user-service/internal/usecase"
	natsgo "github.com/nats-io/nats.go"
)

// CreateUserHandler provisions a user and imports optional OAuth profile data.
type CreateUserHandler struct {
	users    port.UserRepository
	profiles port.UserProfileRepository
	importer service.OAuthProfileImporter
}

// NewCreateUserHandler creates the user provisioning message handler.
func NewCreateUserHandler(users port.UserRepository, profiles port.UserProfileRepository, importer service.OAuthProfileImporter) *CreateUserHandler {
	return &CreateUserHandler{users: users, profiles: profiles, importer: importer}
}

type oauthProfileRequest struct {
	Provider  string `json:"provider"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	BirthYear *int   `json:"birth_year"`
	Gender    string `json:"gender"`
	AvatarURL string `json:"avatar_url"`
}

type createUserRequest struct {
	ID           string               `json:"id"`
	Email        string               `json:"email"`
	Source       string               `json:"source"`
	Type         string               `json:"type"`
	OAuthProfile *oauthProfileRequest `json:"oauth_profile"`
}

// Handle processes user.create-user requests.
func (h *CreateUserHandler) Handle(msg *natsgo.Msg) {
	Respond(msg, h.process(context.Background(), msg.Data))
}

func (h *CreateUserHandler) process(ctx context.Context, payload []byte) map[string]interface{} {
	var req createUserRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return map[string]interface{}{"ok": false, "error": "invalid_payload"}
	}
	if strings.TrimSpace(req.ID) == "" {
		return map[string]interface{}{"ok": false, "error": "id_required"}
	}
	user, err := h.users.FindByID(ctx, req.ID)
	if err != nil && !port.IsNotFound(err) {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	if port.IsNotFound(err) {
		user = &domain.User{ID: req.ID}
		if err := user.SetStatus(domain.UserStatusNew); err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		user.Email = strings.TrimSpace(req.Email)
		if err := h.users.Create(ctx, user); err != nil {
			// A concurrent duplicate is retried through the profile lookup below.
			if existing, findErr := h.users.FindByID(ctx, req.ID); findErr == nil && existing != nil {
				user = existing
			} else {
				return map[string]interface{}{"ok": false, "error": err.Error()}
			}
		}
	}

	profile, err := h.profiles.FindByUserID(ctx, user.ID)
	if err != nil && !port.IsNotFound(err) {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	if port.IsNotFound(err) {
		profile = &domain.UserProfile{UserID: user.ID}
		if err := h.profiles.Create(ctx, profile); err != nil {
			if existing, findErr := h.profiles.FindByUserID(ctx, user.ID); findErr == nil && existing != nil {
				profile = existing
			} else {
				return map[string]interface{}{"ok": false, "error": err.Error()}
			}
		}
	}

	response := map[string]interface{}{"ok": true}
	if req.Type == "oauth" && req.OAuthProfile != nil && h.importer != nil {
		if err := h.importer.Import(ctx, profile, service.OAuthProfileInput{
			Provider: req.OAuthProfile.Provider, FirstName: req.OAuthProfile.FirstName,
			LastName: req.OAuthProfile.LastName, BirthYear: req.OAuthProfile.BirthYear,
			Gender: req.OAuthProfile.Gender, AvatarURL: req.OAuthProfile.AvatarURL,
		}); err != nil {
			response["warning"] = "oauth_profile_import_failed"
		}
	}
	return response
}
