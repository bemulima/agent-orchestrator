package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/example/user-service/internal/infrastructure/imageprocessor"
)

func TestImageProcessorClient_SendsConfiguredInternalToken(t *testing.T) {
	t.Parallel()

	var receivedToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-Internal-Token")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := imageprocessor.NewHTTPClient(server.URL, time.Second, "user-to-image-processor-token")
	require.NoError(t, client.GenerateWithDelegation(
		context.Background(), "uploaded-file-987", "authenticated-user-17", "USER_MEDIA", "avatar", nil, "opaque-grant",
	))
	require.Equal(t, "user-to-image-processor-token", receivedToken)
}
