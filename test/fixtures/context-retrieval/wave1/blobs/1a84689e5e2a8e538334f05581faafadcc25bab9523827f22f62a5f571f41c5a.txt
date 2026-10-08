package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/example/auth-service/internal/domain"
)

func (s *authService) ListIdentities(ctx context.Context, traceID, userID string) ([]domain.AuthIdentity, error) {
	identities, err := s.identities.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", userID).Int("count", len(identities)).Msg("oauth identities loaded")
	return identities, nil
}

func (s *authService) RemoveIdentity(ctx context.Context, traceID, userID, provider, providerUserID string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	providerUserID = strings.TrimSpace(providerUserID)
	if provider == "" || providerUserID == "" {
		return fmt.Errorf("provider and provider user id are required")
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	identities, err := s.identities.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if user.PasswordHash == nil && len(identities) <= 1 {
		return fmt.Errorf("set a password or connect another provider before removing the last identity")
	}
	if err := s.identities.Delete(ctx, userID, provider, providerUserID); err != nil {
		return err
	}
	s.logger.Info().Str("trace_id", traceID).Str("user_id", userID).Str("provider", provider).Msg("oauth identity removed")
	return nil
}
