package service

import (
	"context"
	"errors"
	"testing"
)

type activeUserRepoStub struct {
	active []string
	calls  int
	ids    []string
	err    error
}

func (s *activeUserRepoStub) ListActiveIDs(_ context.Context, ids []string) ([]string, error) {
	s.calls++
	s.ids = append([]string(nil), ids...)
	return append([]string(nil), s.active...), s.err
}

func TestActiveUserServiceResolvesInRequestOrder(t *testing.T) {
	first := "11111111-1111-4111-8111-111111111111"
	second := "22222222-2222-4222-8222-222222222222"
	third := "33333333-3333-4333-8333-333333333333"
	repo := &activeUserRepoStub{active: []string{third, first}}
	result, err := NewActiveUserService(repo).ResolveActiveUsers(context.Background(), []string{first, second, third})
	if err != nil {
		t.Fatalf("resolve active users: %v", err)
	}
	if repo.calls != 1 || len(repo.ids) != 3 {
		t.Fatalf("repository call mismatch: calls=%d ids=%v", repo.calls, repo.ids)
	}
	if len(result.ActiveUserIDs) != 2 || result.ActiveUserIDs[0] != first || result.ActiveUserIDs[1] != third {
		t.Fatalf("active order mismatch: %v", result.ActiveUserIDs)
	}
	if len(result.UnavailableUserIDs) != 1 || result.UnavailableUserIDs[0] != second {
		t.Fatalf("unavailable users mismatch: %v", result.UnavailableUserIDs)
	}
}

func TestActiveUserServiceRejectsInvalidBatches(t *testing.T) {
	valid := "11111111-1111-4111-8111-111111111111"
	tests := map[string][]string{
		"empty":     nil,
		"invalid":   {"not-a-uuid"},
		"nil uuid":  {"00000000-0000-0000-0000-000000000000"},
		"duplicate": {valid, valid},
		"too large": make([]string, MaxActiveUserBatch+1),
	}
	for name, ids := range tests {
		t.Run(name, func(t *testing.T) {
			repo := &activeUserRepoStub{}
			_, err := NewActiveUserService(repo).ResolveActiveUsers(context.Background(), ids)
			if !errors.Is(err, ErrInvalidActiveUserBatch) {
				t.Fatalf("expected invalid batch, got %v", err)
			}
			if repo.calls != 0 {
				t.Fatalf("repository called for invalid batch")
			}
		})
	}
}

func TestActiveUserServicePropagatesRepositoryFailure(t *testing.T) {
	want := errors.New("database unavailable")
	repo := &activeUserRepoStub{err: want}
	_, err := NewActiveUserService(repo).ResolveActiveUsers(context.Background(), []string{"11111111-1111-4111-8111-111111111111"})
	if !errors.Is(err, want) {
		t.Fatalf("expected repository failure, got %v", err)
	}
}
