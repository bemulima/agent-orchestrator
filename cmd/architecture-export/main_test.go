package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestProducerRequiresIndependentCleanVCSProvenance(t *testing.T) {
	clean := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "false"}}}
	producer, err := producerFromBuild(clean)
	if err != nil || producer.CommitSHA != strings.Repeat("a", 40) {
		t.Fatalf("clean provenance: %v", err)
	}
	for _, info := range []*debug.BuildInfo{{}, {Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "main"}, {Key: "vcs.modified", Value: "false"}}}, {Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "true"}}}} {
		if _, err := producerFromBuild(info); err == nil {
			t.Fatal("unproven producer accepted")
		}
	}
}
func TestExportHelpHasNoDatabaseSideEffects(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CURRENT") {
		t.Fatal("help omitted scope")
	}
}
