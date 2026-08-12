package templates

import "testing"

func TestBundleIsCompleteAndStable(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	if len(Checksum()) != 64 {
		t.Fatalf("checksum = %q", Checksum())
	}
}

func TestBackendCoderExpandsVerificationPolicy(t *testing.T) {
	content := BackendCoder("run the verified command")
	if content == "" || content == BackendCoder("another policy") {
		t.Fatalf("backend coder template was not expanded: %q", content)
	}
}
