package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"
)

const (
	githubUserURL   = "https://api.github.com/user"
	githubEmailsURL = "https://api.github.com/user/emails"
)

type GitHubOAuth2 struct {
	*OAuth2Base
	userURL   string
	emailsURL string
}

func NewGitHubOAuth2(clientID, clientSecret, redirectURL string) *GitHubOAuth2 {
	return &GitHubOAuth2{
		OAuth2Base: &OAuth2Base{
			ProviderName: ProviderGitHub,
			HTTPClient:   defaultHTTPClient(),
			Config: oauth2.Config{
				ClientID:     clientID,
				ClientSecret: clientSecret,
				RedirectURL:  redirectURL,
				Scopes:       []string{"read:user", "user:email"},
				Endpoint:     githuboauth.Endpoint,
			},
		},
		userURL:   githubUserURL,
		emailsURL: githubEmailsURL,
	}
}

func (p *GitHubOAuth2) Authenticate(ctx context.Context, code, verifier string) (*Profile, error) {
	token, err := p.Exchange(ctx, code, verifier)
	if err != nil {
		return nil, fmt.Errorf("github token exchange failed: %w", err)
	}
	client := p.Client(ctx, token)

	var userPayload struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := getGitHubJSON(ctx, client, p.userURL, &userPayload); err != nil {
		return nil, err
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getGitHubJSON(ctx, client, p.emailsURL, &emails); err != nil {
		return nil, err
	}

	email := ""
	for _, item := range emails {
		if item.Primary && item.Verified && strings.TrimSpace(item.Email) != "" {
			email = strings.TrimSpace(item.Email)
			break
		}
	}
	if email == "" {
		for _, item := range emails {
			if item.Verified && strings.TrimSpace(item.Email) != "" {
				email = strings.TrimSpace(item.Email)
				break
			}
		}
	}

	displayName := strings.TrimSpace(userPayload.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(userPayload.Login)
	}

	return &Profile{
		ProviderUserID: fmt.Sprintf("%d", userPayload.ID),
		Email:          email,
		EmailVerified:  email != "",
		DisplayName:    displayName,
		AvatarURL:      strings.TrimSpace(userPayload.AvatarURL),
		RawProfile: map[string]interface{}{
			"id":         userPayload.ID,
			"login":      userPayload.Login,
			"name":       userPayload.Name,
			"email":      email,
			"avatar_url": userPayload.AvatarURL,
		},
	}, nil
}

func getGitHubJSON(ctx context.Context, client *http.Client, endpoint string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ms-go-auth")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github api request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("github api request failed: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}
