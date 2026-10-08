// Package service contains user-domain application services.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/example/user-service/internal/port"
)

// MaxActiveUserBatch bounds one service-to-service resolution request.
const MaxActiveUserBatch = 1000

// ErrInvalidActiveUserBatch marks malformed, empty, duplicate, or oversized batches.
var ErrInvalidActiveUserBatch = errors.New("invalid active user batch")

// ActiveUserResolver partitions requested UUIDs into active and unavailable IDs.
type ActiveUserResolver interface {
	ResolveActiveUsers(ctx context.Context, userIDs []string) (ActiveUserResolution, error)
}

// ActiveUserResolution contains ordered UUID-only results without profile data.
type ActiveUserResolution struct {
	ActiveUserIDs      []string `json:"active_user_ids"`
	UnavailableUserIDs []string `json:"unavailable_user_ids"`
}

type activeUserService struct {
	users port.ActiveUserRepository
}

// NewActiveUserService creates the bounded active-user resolver.
func NewActiveUserService(users port.ActiveUserRepository) ActiveUserResolver {
	return &activeUserService{users: users}
}

func (s *activeUserService) ResolveActiveUsers(ctx context.Context, userIDs []string) (ActiveUserResolution, error) {
	if s == nil || s.users == nil || len(userIDs) < 1 || len(userIDs) > MaxActiveUserBatch {
		return ActiveUserResolution{}, ErrInvalidActiveUserBatch
	}
	normalized := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, raw := range userIDs {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return ActiveUserResolution{}, fmt.Errorf("%w: invalid UUID", ErrInvalidActiveUserBatch)
		}
		value := id.String()
		if _, exists := seen[value]; exists {
			return ActiveUserResolution{}, fmt.Errorf("%w: duplicate UUID", ErrInvalidActiveUserBatch)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	activeIDs, err := s.users.ListActiveIDs(ctx, normalized)
	if err != nil {
		return ActiveUserResolution{}, err
	}
	activeSet := make(map[string]struct{}, len(activeIDs))
	for _, raw := range activeIDs {
		if id, parseErr := uuid.Parse(raw); parseErr == nil {
			activeSet[id.String()] = struct{}{}
		}
	}
	result := ActiveUserResolution{
		ActiveUserIDs:      make([]string, 0, len(normalized)),
		UnavailableUserIDs: make([]string, 0),
	}
	for _, id := range normalized {
		if _, active := activeSet[id]; active {
			result.ActiveUserIDs = append(result.ActiveUserIDs, id)
		} else {
			result.UnavailableUserIDs = append(result.UnavailableUserIDs, id)
		}
	}
	return result, nil
}
