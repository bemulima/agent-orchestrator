package unit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/example/user-service/internal/infrastructure/filestorage"
	"github.com/example/user-service/internal/infrastructure/imageprocessor"
)

type processingDelegationIssuerContract interface {
	CreateProcessingDelegation(ctx context.Context, fileID, ownerID, delegateService, scope string, ttlSeconds int64) (string, error)
}

type delegatedImageGeneratorContract interface {
	GenerateWithDelegation(ctx context.Context, originalID, ownerID, fileKind, presetGroup string, variants []string, processingDelegation string) error
}

func TestFileStorageClient_MintsProcessingDelegationUsingFrozenV21Contract(t *testing.T) {
	t.Parallel()

	var method, requestPath, internalToken string
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.Path
		internalToken = r.Header.Get("X-Internal-Token")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delegation":"opaque-file-processing-grant","expires_at":"2030-01-02T03:04:05Z"}`))
	}))
	defer server.Close()

	client := filestorage.NewHTTPClient(server.URL, "user-to-filestorage-internal-token", time.Second)
	issuer, ok := client.(processingDelegationIssuerContract)
	require.True(t, ok, "FileStorage client must expose processing-delegation minting")
	grant, err := issuer.CreateProcessingDelegation(context.Background(), "uploaded-file-987", "authenticated-user-17", "image_processor", "read_source", 0)
	require.NoError(t, err)
	require.Equal(t, "opaque-file-processing-grant", grant)
	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "/internal/v1/files/uploaded-file-987/processing-delegations", requestPath)
	require.Equal(t, "user-to-filestorage-internal-token", internalToken)
	require.Equal(t, map[string]any{
		"owner_id":         "authenticated-user-17",
		"delegate_service": "image_processor",
		"scope":            "read_source",
	}, requestBody, "owner assertion, delegate and scope use the frozen v2.1 JSON field names; omitted TTL selects the 300 second default")
}

func TestImageProcessorClient_SendsProcessingDelegationField(t *testing.T) {
	t.Parallel()

	var method, requestPath string
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		requestPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := imageprocessor.NewHTTPClient(server.URL, time.Second)
	generator, ok := client.(delegatedImageGeneratorContract)
	require.True(t, ok, "Image Processor client must support a scoped processing delegation")
	require.NoError(t, generator.GenerateWithDelegation(context.Background(), "uploaded-file-987", "authenticated-user-17", "USER_MEDIA", "avatar", nil, "opaque-file-processing-grant"))

	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "/admin/v1/images/uploaded-file-987/variants/generate", requestPath)
	require.Equal(t, "authenticated-user-17", requestBody["owner_id"])
	require.Equal(t, "USER_MEDIA", requestBody["file_kind"])
	require.Equal(t, "avatar", requestBody["preset_group"])
	require.Equal(t, "opaque-file-processing-grant", requestBody["processing_delegation"])
	require.NotContains(t, requestBody, "delegation", "the wire key is processing_delegation")
}
