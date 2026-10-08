package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
)

const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

type GoogleOAuth2 struct {
	*OAuth2Base
	userInfoURL string
}

func NewGoogleOAuth2(clientID, clientSecret, redirectURL string) *GoogleOAuth2 {
	return &GoogleOAuth2{
		OAuth2Base: &OAuth2Base{
			ProviderName: ProviderGoogle,
			HTTPClient:   defaultHTTPClient(),
			Config: oauth2.Config{
				ClientID:     clientID,
				ClientSecret: clientSecret,
				RedirectURL:  redirectURL,
				Scopes:       []string{"openid", "email", "profile"},
				Endpoint:     googleoauth.Endpoint,
			},
		},
		userInfoURL: googleUserInfoURL,
	}
}

func (p *GoogleOAuth2) Authenticate(ctx context.Context, code, verifier string) (*Profile, error) {
	token, err := p.Exchange(ctx, code, verifier)
	if err != nil {
		return nil, fmt.Errorf("google token exchange failed: %w", err)
	}

	client := p.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google userinfo failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("google userinfo failed: %s", resp.Status)
	}

	var payload struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		Name          string `json:"name"`
		GivenName     string `json:"given_name"`
		FamilyName    string `json:"family_name"`
		Birthdate     string `json:"birthdate"`
		Gender        string `json:"gender"`
		EmailVerified bool   `json:"email_verified"`
		Picture       string `json:"picture"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode google userinfo: %w", err)
	}

	return &Profile{
		ProviderUserID: strings.TrimSpace(payload.Sub),
		Email:          strings.TrimSpace(payload.Email),
		EmailVerified:  payload.EmailVerified,
		DisplayName:    strings.TrimSpace(payload.Name),
		FirstName:      strings.TrimSpace(payload.GivenName),
		LastName:       strings.TrimSpace(payload.FamilyName),
		BirthYear:      birthYearFromClaim(payload.Birthdate),
		Gender:         strings.ToLower(strings.TrimSpace(payload.Gender)),
		AvatarURL:      strings.TrimSpace(payload.Picture),
		RawProfile: map[string]interface{}{
			"sub":            payload.Sub,
			"email":          payload.Email,
			"name":           payload.Name,
			"given_name":     payload.GivenName,
			"family_name":    payload.FamilyName,
			"birthdate":      payload.Birthdate,
			"gender":         payload.Gender,
			"email_verified": payload.EmailVerified,
			"picture":        payload.Picture,
		},
	}, nil
}
