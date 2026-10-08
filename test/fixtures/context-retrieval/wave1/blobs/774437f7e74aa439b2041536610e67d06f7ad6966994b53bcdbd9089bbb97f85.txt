package oauthavatar

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClientDownloadsAllowedProviderImage(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(png)
	}))
	defer server.Close()

	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client := NewHTTPClient(time.Second, DefaultMaxSize)
	client.client = &http.Client{Transport: transport, Timeout: time.Second}

	image, err := client.Download(context.Background(), "github", "https://avatars.githubusercontent.com/u/1?v=4")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if image.ContentType != "image/png" || image.FileName != "oauth-avatar.png" || len(image.Data) != len(png) {
		t.Fatalf("unexpected image: %+v", image)
	}
}

func TestHTTPClientRejectsUntrustedAvatarURL(t *testing.T) {
	client := NewHTTPClient(time.Second, DefaultMaxSize)
	cases := []struct {
		provider string
		url      string
	}{
		{provider: "github", url: "https://example.com/avatar.png"},
		{provider: "google", url: "http://lh3.googleusercontent.com/avatar.png"},
		{provider: "google", url: "https://googleusercontent.com.evil.example/avatar.png"},
		{provider: "unknown", url: "https://avatars.githubusercontent.com/u/1"},
	}
	for _, tc := range cases {
		if _, err := client.Download(context.Background(), tc.provider, tc.url); err == nil {
			t.Fatalf("expected %s URL %q to be rejected", tc.provider, tc.url)
		}
	}
}
