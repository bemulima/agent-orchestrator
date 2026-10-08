package handlers

import (
	"testing"

	"github.com/example/user-service/internal/domain"
)

func TestMaskEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
		want  string
	}{
		{name: "common", email: "admin@example.com", want: "a****@***e.com"},
		{name: "spaces", email: "  user@test.dev ", want: "u****@***t.dev"},
		{name: "invalid", email: "invalid", want: "invalid"},
		{name: "empty-local", email: "@example.com", want: "@example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskEmail(tc.email); got != tc.want {
				t.Fatalf("maskEmail(%q) = %q, want %q", tc.email, got, tc.want)
			}
		})
	}
}

func TestSensitiveOAuthProfileFieldsAreOnlyReturnedToCurrentUser(t *testing.T) {
	firstName := "Ada"
	lastName := "Lovelace"
	birthYear := 1998
	gender := "female"
	user := &domain.User{ID: "user-1", Email: "ada@example.com", Profile: &domain.UserProfile{
		FirstName: &firstName, LastName: &lastName, BirthYear: &birthYear, Gender: &gender,
	}}
	handler := &Handler{}

	self := handler.newSelfUserResponse(user)
	if self.FirstName == nil || self.LastName == nil || self.BirthYear == nil || self.Gender == nil {
		t.Fatalf("current user response must include OAuth profile fields: %+v", self)
	}
	public := handler.newPublicUserResponse(user)
	if public.FirstName != nil || public.LastName != nil || public.BirthYear != nil || public.Gender != nil {
		t.Fatalf("public response must not expose sensitive OAuth profile fields: %+v", public)
	}
}
