package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

func TestGoogleOAuth2NormalizesProfileFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"access_token":"token","token_type":"Bearer"}`)
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"sub":"google-1","email":"user@example.com","email_verified":true,"name":"Ada Lovelace","given_name":" Ada ","family_name":" Lovelace ","birthdate":"1998-12-10","gender":"FEMALE","picture":"https://lh3.googleusercontent.com/a/avatar"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewGoogleOAuth2("client", "secret", "http://localhost/callback")
	provider.Config.Endpoint = oauth2.Endpoint{AuthURL: server.URL + "/auth", TokenURL: server.URL + "/token"}
	provider.HTTPClient = server.Client()
	provider.userInfoURL = server.URL + "/userinfo"

	profile, err := provider.Authenticate(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if profile.FirstName != "Ada" || profile.LastName != "Lovelace" || profile.BirthYear == nil || *profile.BirthYear != 1998 || profile.Gender != "female" || profile.AvatarURL != "https://lh3.googleusercontent.com/a/avatar" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestGitHubOAuth2KeepsCombinedNameOutOfStructuredFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprint(w, `{"access_token":"token","token_type":"bearer"}`)
		case "/user":
			_, _ = fmt.Fprint(w, `{"id":1,"login":"ada","name":"Ada Lovelace","avatar_url":"https://avatars.githubusercontent.com/u/1?v=4"}`)
		case "/emails":
			_, _ = fmt.Fprint(w, `[{"email":"user@example.com","primary":true,"verified":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewGitHubOAuth2("client", "secret", "http://localhost/callback")
	provider.Config.Endpoint = oauth2.Endpoint{AuthURL: server.URL + "/auth", TokenURL: server.URL + "/token"}
	provider.HTTPClient = server.Client()
	provider.userURL = server.URL + "/user"
	provider.emailsURL = server.URL + "/emails"

	profile, err := provider.Authenticate(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if profile.DisplayName != "Ada Lovelace" || profile.FirstName != "" || profile.LastName != "" || profile.AvatarURL != "https://avatars.githubusercontent.com/u/1?v=4" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}
