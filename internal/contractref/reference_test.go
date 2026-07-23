package contractref

import "testing"

func TestHTTP(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantMethod string
		wantPath   string
		wantOK     bool
	}{
		{name: "method and path", value: "POST /api/v1/items", wantMethod: "POST", wantPath: "/api/v1/items", wantOK: true},
		{name: "any method", value: "ANY /health", wantMethod: "ANY", wantPath: "/health", wantOK: true},
		{name: "frontend path", value: "/api/session/status?fresh=1", wantMethod: "GET", wantPath: "/api/session/status", wantOK: true},
		{name: "absolute URL", value: "GET https://service.local/api/v1/items", wantMethod: "GET", wantPath: "/api/v1/items", wantOK: true},
		{name: "templated base", value: "GET {HTTP_BASE_PATH}/stat/events", wantMethod: "GET", wantPath: "/{HTTP_BASE_PATH}/stat/events", wantOK: true},
		{name: "response prose", value: "GET /health returns status", wantOK: false},
		{name: "header prose", value: "X-User-Id and X-User-Role headers", wantOK: false},
		{name: "unexpanded URL", value: "GET http://127.0.0.1:${port}", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			method, path, ok := HTTP(test.value)
			if method != test.wantMethod || path != test.wantPath || ok != test.wantOK {
				t.Fatalf("HTTP(%q) = %q, %q, %v; want %q, %q, %v", test.value, method, path, ok, test.wantMethod, test.wantPath, test.wantOK)
			}
		})
	}
}

func TestEventSubject(t *testing.T) {
	for _, value := range []string{"orders.created.v1", "sandbox.archiveandupload", "$JS.API.STREAM.INFO.*"} {
		if _, ok := EventSubject(value); !ok {
			t.Fatalf("EventSubject(%q) rejected a subject", value)
		}
	}
	for _, value := range []string{"NATS queue request/reply", "rbac.checkRole request: user_id, role", "AUTH/LOGGED_IN"} {
		if _, ok := EventSubject(value); ok {
			t.Fatalf("EventSubject(%q) accepted descriptive text", value)
		}
	}
}
