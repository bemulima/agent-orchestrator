package agentcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

type GlobalPolicyState string

const (
	GlobalPolicyMissing GlobalPolicyState = "missing"
	GlobalPolicyCurrent GlobalPolicyState = "current"
	GlobalPolicyDiffers GlobalPolicyState = "differs"
)

type GlobalPolicyProposal struct {
	TargetPath       string            `json:"target_path"`
	State            GlobalPolicyState `json:"state"`
	SourceChecksum   string            `json:"source_checksum"`
	CurrentChecksum  string            `json:"current_checksum,omitempty"`
	Fingerprint      string            `json:"proposal_fingerprint"`
	Diff             string            `json:"diff,omitempty"`
	canonicalContent []byte
}

var ErrGlobalPolicyInstallUnavailable = errors.New("global policy installation is unavailable; proposal and diff are read-only")

func BuildGlobalPolicyProposal(catalog Catalog, targetPath string, current []byte, exists bool) (GlobalPolicyProposal, error) {
	if strings.TrimSpace(targetPath) == "" {
		return GlobalPolicyProposal{}, errors.New("explicit global policy target path is required")
	}
	var source *ResolvedAsset
	for index := range catalog.Assets {
		if catalog.Assets[index].Asset.ID == "global-policy" && catalog.Assets[index].Asset.Kind == AssetKindGlobalPolicy {
			source = &catalog.Assets[index]
			break
		}
	}
	if source == nil {
		return GlobalPolicyProposal{}, errors.New("canonical global-policy asset is missing")
	}
	content, ok := source.Files["AGENTS.md"]
	if !ok {
		return GlobalPolicyProposal{}, errors.New("canonical global-policy asset has no AGENTS.md")
	}
	proposal := GlobalPolicyProposal{TargetPath: path.Clean(targetPath), SourceChecksum: source.Checksum, canonicalContent: append([]byte(nil), content...)}
	switch {
	case !exists:
		proposal.State = GlobalPolicyMissing
		proposal.Diff = diffFiles(proposal.TargetPath, nil, false, map[string][]byte{path.Base(proposal.TargetPath): content})
	case string(current) == string(content):
		proposal.State = GlobalPolicyCurrent
		proposal.CurrentChecksum = ChecksumFiles(map[string][]byte{path.Base(proposal.TargetPath): current})
	default:
		proposal.State = GlobalPolicyDiffers
		proposal.CurrentChecksum = ChecksumFiles(map[string][]byte{path.Base(proposal.TargetPath): current})
		proposal.Diff = diffFiles(proposal.TargetPath, map[string][]byte{path.Base(proposal.TargetPath): current}, true, map[string][]byte{path.Base(proposal.TargetPath): content})
	}
	material, err := json.Marshal(struct {
		TargetPath string            `json:"target_path"`
		State      GlobalPolicyState `json:"state"`
		Source     string            `json:"source_checksum"`
		Current    string            `json:"current_checksum,omitempty"`
	}{proposal.TargetPath, proposal.State, proposal.SourceChecksum, proposal.CurrentChecksum})
	if err != nil {
		return GlobalPolicyProposal{}, fmt.Errorf("fingerprint global policy proposal: %w", err)
	}
	digest := sha256.Sum256(material)
	proposal.Fingerprint = "sha256:" + hex.EncodeToString(digest[:])
	return proposal, nil
}

// ApplyGlobalPolicy intentionally remains fail-closed. A dedicated high-trust
// approval and user-config writer must be connected before this can mutate a
// home-directory target.
func ApplyGlobalPolicy(context.Context, GlobalPolicyProposal, string) error {
	return ErrGlobalPolicyInstallUnavailable
}
