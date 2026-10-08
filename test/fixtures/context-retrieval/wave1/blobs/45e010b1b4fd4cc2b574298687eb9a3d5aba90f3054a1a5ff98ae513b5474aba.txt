package nats

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/example/user-service/internal/domain"
	service "github.com/example/user-service/internal/usecase"
)

type createUserRepoStub struct {
	user *domain.User
}

func (r *createUserRepoStub) Create(_ context.Context, user *domain.User) error {
	r.user = user
	return nil
}
func (r *createUserRepoStub) Update(context.Context, *domain.User) error { return nil }
func (r *createUserRepoStub) FindByEmail(context.Context, string) (*domain.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *createUserRepoStub) FindByID(_ context.Context, id string) (*domain.User, error) {
	if r.user != nil && r.user.ID == id {
		return r.user, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *createUserRepoStub) Delete(context.Context, string) error { return nil }
func (r *createUserRepoStub) List(context.Context, int, int) ([]domain.User, int64, error) {
	return nil, 0, nil
}

type createProfileRepoStub struct {
	profile *domain.UserProfile
}

func (r *createProfileRepoStub) Create(_ context.Context, profile *domain.UserProfile) error {
	r.profile = profile
	return nil
}
func (r *createProfileRepoStub) Update(context.Context, *domain.UserProfile) error { return nil }
func (r *createProfileRepoStub) FindByUserID(context.Context, string) (*domain.UserProfile, error) {
	if r.profile == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return r.profile, nil
}

type createOAuthImporterStub struct {
	calls int
	input service.OAuthProfileInput
	err   error
}

func (i *createOAuthImporterStub) Import(_ context.Context, _ *domain.UserProfile, input service.OAuthProfileInput) error {
	i.calls++
	i.input = input
	return i.err
}

func TestCreateUserHandlerImportsOAuthProfileForNewUser(t *testing.T) {
	users := &createUserRepoStub{}
	profiles := &createProfileRepoStub{}
	importer := &createOAuthImporterStub{}
	handler := NewCreateUserHandler(users, profiles, importer)
	birthYear := 1998
	payload, _ := json.Marshal(createUserRequest{
		ID: "user-1", Email: "user@example.com", Source: "auth", Type: "oauth",
		OAuthProfile: &oauthProfileRequest{Provider: "google", FirstName: "Ada", LastName: "Lovelace", BirthYear: &birthYear, Gender: "female", AvatarURL: "https://lh3.googleusercontent.com/a/avatar"},
	})

	response := handler.process(context.Background(), payload)
	if response["ok"] != true || importer.calls != 1 || profiles.profile == nil {
		t.Fatalf("OAuth profile was not imported: response=%+v calls=%d", response, importer.calls)
	}
	if importer.input.FirstName != "Ada" || importer.input.BirthYear == nil || *importer.input.BirthYear != birthYear {
		t.Fatalf("unexpected importer input: %+v", importer.input)
	}
}

func TestCreateUserHandlerDoesNotFailRegistrationWhenAvatarImportFails(t *testing.T) {
	handler := NewCreateUserHandler(&createUserRepoStub{}, &createProfileRepoStub{}, &createOAuthImporterStub{err: errors.New("download failed")})
	payload := []byte(`{"id":"user-1","email":"user@example.com","type":"oauth","oauth_profile":{"provider":"github","avatar_url":"https://avatars.githubusercontent.com/u/1"}}`)

	response := handler.process(context.Background(), payload)
	if response["ok"] != true || response["warning"] != "oauth_profile_import_failed" {
		t.Fatalf("import failure must be non-blocking: %+v", response)
	}
}

func TestCreateUserHandlerImportsOnlyMissingDataIntoExistingProfile(t *testing.T) {
	users := &createUserRepoStub{user: &domain.User{ID: "user-1", Email: "user@example.com"}}
	manualFirstName := "Manual"
	profiles := &createProfileRepoStub{profile: &domain.UserProfile{UserID: "user-1", FirstName: &manualFirstName}}
	importer := &createOAuthImporterStub{}
	handler := NewCreateUserHandler(users, profiles, importer)
	payload := []byte(`{"id":"user-1","email":"user@example.com","type":"oauth","oauth_profile":{"provider":"google","first_name":"Provider","last_name":"Name"}}`)

	response := handler.process(context.Background(), payload)
	if response["ok"] != true || importer.calls != 1 || importer.input.LastName != "Name" {
		t.Fatalf("existing profile must receive missing OAuth data: response=%+v calls=%d input=%+v", response, importer.calls, importer.input)
	}
}
