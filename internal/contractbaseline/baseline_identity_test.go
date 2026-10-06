package contractbaseline

import "testing"

func TestBaselineIDVersionsPreserveInitialIdentityAndSeparateRetiredHistory(t *testing.T) {
	initial := BaselineID("plan-1", "task-1", "project-1")
	if initial != "bcf0e651-7f9f-5362-b5eb-5c2fdabcb980" {
		t.Fatalf("initial baseline identity changed: %s", initial)
	}
	replacement := BaselineID("plan-1", "task-1", "project-1", initial)
	if replacement == initial || replacement != BaselineID("plan-1", "task-1", "project-1", initial) {
		t.Fatal("replacement identity is reused or nondeterministic")
	}
	next := BaselineID("plan-1", "task-1", "project-1", replacement)
	if next == initial || next == replacement {
		t.Fatal("successive retirements reused a historical identity")
	}
}
