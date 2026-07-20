package health

import (
	"context"
	"errors"
	"testing"
)

type fakeChecker struct {
	name string
	err  error
}

func (f fakeChecker) Name() string               { return f.name }
func (f fakeChecker) Ping(context.Context) error { return f.err }

func TestCheckReadiness_Handle(t *testing.T) {
	tests := []struct {
		name         string
		dependencies []fakeChecker
		wantStatus   string
	}{
		{
			name: "all dependencies available",
			dependencies: []fakeChecker{
				{name: "postgres"},
				{name: "temporal"},
			},
			wantStatus: StatusOK,
		},
		{
			name: "one dependency unavailable",
			dependencies: []fakeChecker{
				{name: "postgres"},
				{name: "temporal", err: errors.New("contains private connection details")},
			},
			wantStatus: StatusDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := CheckReadiness{}
			for _, dependency := range tt.dependencies {
				uc.Dependencies = append(uc.Dependencies, dependency)
			}
			result := uc.Handle(context.Background())
			if result.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, tt.wantStatus)
			}
			for _, status := range result.Dependencies {
				if status.Error != "" && status.Error != "unavailable" {
					t.Fatalf("dependency error leaked internal details: %q", status.Error)
				}
			}
		})
	}
}
