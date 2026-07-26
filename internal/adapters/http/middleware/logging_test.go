package middleware

import (
	"net/http/httptest"
	"testing"
)

func TestResponseRecorderPreservesStreaming(t *testing.T) {
	response := httptest.NewRecorder()
	recorder := &responseRecorder{ResponseWriter: response}
	recorder.Flush()
	if !response.Flushed {
		t.Fatal("Flush was not delegated to the wrapped response writer")
	}
}
