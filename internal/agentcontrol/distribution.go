package agentcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"gopkg.in/yaml.v3"
)

type AssetState string

const (
	AssetStateMissing         AssetState = "missing"
	AssetStateCurrent         AssetState = "current"
	AssetStateStale           AssetState = "stale"
	AssetStateLocallyModified AssetState = "locally_modified"
	AssetStateConflicting     AssetState = "conflicting"
)

type FileChecksum struct {
	Path     string `yaml:"path" json:"path"`
	Checksum string `yaml:"checksum" json:"checksum"`
}

type ProvenanceEntry struct {
	AssetID    string         `yaml:"asset_id" json:"asset_id"`
	Kind       AssetKind      `yaml:"kind" json:"kind"`
	SourcePath string         `yaml:"source_path" json:"source_path"`
	Ownership  Ownership      `yaml:"ownership" json:"ownership"`
	Version    string         `yaml:"version" json:"version"`
	TargetPath string         `yaml:"target_path" json:"target_path"`
	Checksum   string         `yaml:"checksum" json:"checksum"`
	Files      []FileChecksum `yaml:"files" json:"files"`
}

// DistributionManifest is generated at the target repository. Its entries
// record managed provenance; unlisted skills remain repository-local.
type DistributionManifest struct {
	SchemaVersion int               `yaml:"schema_version" json:"schema_version"`
	Ownership     Ownership         `yaml:"ownership" json:"ownership"`
	Generator     string            `yaml:"generator" json:"generator"`
	Assets        []ProvenanceEntry `yaml:"managed_assets" json:"managed_assets"`
}

type TargetAsset struct {
	Exists bool              `json:"exists"`
	Unsafe bool              `json:"unsafe,omitempty"`
	Files  map[string][]byte `json:"-"`
}

type RepositorySnapshot struct {
	Root              string                 `json:"repository_root"`
	Revision          string                 `json:"base_revision"`
	Assets            map[string]TargetAsset `json:"assets"`
	Provenance        DistributionManifest   `json:"provenance"`
	HasProvenance     bool                   `json:"has_provenance"`
	ProvenanceContent []byte                 `json:"-"`
}

type AssetChange struct {
	AssetID          string        `json:"asset_id"`
	TargetPath       string        `json:"target_path"`
	State            AssetState    `json:"state"`
	CurrentChecksum  string        `json:"current_checksum,omitempty"`
	PreviousChecksum string        `json:"previous_checksum,omitempty"`
	DesiredChecksum  string        `json:"desired_checksum"`
	Diff             string        `json:"diff,omitempty"`
	Source           ResolvedAsset `json:"-"`
	Current          TargetAsset   `json:"-"`
}

type Proposal struct {
	SchemaVersion  int                  `json:"schema_version"`
	RepositoryRoot string               `json:"repository_root"`
	BaseRevision   string               `json:"base_revision"`
	CatalogDigest  string               `json:"catalog_digest"`
	Fingerprint    string               `json:"proposal_fingerprint"`
	Changes        []AssetChange        `json:"changes"`
	RetainedAssets []string             `json:"retained_assets,omitempty"`
	ManifestDiff   string               `json:"manifest_diff,omitempty"`
	Diff           string               `json:"diff,omitempty"`
	NextManifest   DistributionManifest `json:"next_manifest"`
}

type ApplyResult struct {
	ProposalFingerprint string   `json:"proposal_fingerprint"`
	Written             []string `json:"written"`
	Removed             []string `json:"removed,omitempty"`
	ProvenancePath      string   `json:"provenance_path"`
}

var (
	ErrApprovalRequired = errors.New("distribution apply requires the exact approved proposal fingerprint")
	ErrApplyConflict    = errors.New("distribution proposal contains conflicts; no files were applied")
)

func (proposal Proposal) Change(assetID string) AssetChange {
	for _, change := range proposal.Changes {
		if change.AssetID == assetID {
			return change
		}
	}
	return AssetChange{}
}

func BuildDistributionProposal(catalog Catalog, snapshot RepositorySnapshot) (Proposal, error) {
	if strings.TrimSpace(snapshot.Root) == "" || strings.TrimSpace(snapshot.Revision) == "" {
		return Proposal{}, errors.New("repository snapshot requires canonical root and base revision")
	}
	if err := ValidateDistributionManifest(snapshot.Provenance, snapshot.HasProvenance); err != nil {
		return Proposal{}, err
	}
	previous := make(map[string]ProvenanceEntry, len(snapshot.Provenance.Assets))
	for _, entry := range snapshot.Provenance.Assets {
		previous[entry.AssetID] = entry
	}
	desired := make(map[string]struct{}, len(catalog.Assets))
	proposal := Proposal{
		SchemaVersion:  ManifestSchemaVersion,
		RepositoryRoot: snapshot.Root,
		BaseRevision:   snapshot.Revision,
		CatalogDigest:  catalog.Digest,
	}
	for _, asset := range catalog.Assets {
		if asset.Asset.Kind == AssetKindGlobalPolicy {
			continue
		}
		desired[asset.Asset.ID] = struct{}{}
		actual := snapshot.Assets[asset.Asset.ID]
		prior, hasPrior := previous[asset.Asset.ID]
		change := AssetChange{
			AssetID: asset.Asset.ID, TargetPath: asset.Asset.TargetPath,
			DesiredChecksum: asset.Checksum, Source: asset, Current: actual,
		}
		if actual.Exists && !actual.Unsafe {
			change.CurrentChecksum = ChecksumFiles(actual.Files)
		}
		if hasPrior {
			change.PreviousChecksum = prior.Checksum
		}
		switch {
		case hasPrior && (prior.Ownership != OwnershipManaged || prior.TargetPath != asset.Asset.TargetPath || prior.SourcePath != asset.Asset.SourcePath):
			change.State = AssetStateConflicting
		case actual.Unsafe:
			change.State = AssetStateConflicting
		case !actual.Exists:
			change.State = AssetStateMissing
		case change.CurrentChecksum == asset.Checksum:
			change.State = AssetStateCurrent
		case !hasPrior:
			change.State = AssetStateConflicting
		case matchesProvenance(actual.Files, prior):
			change.State = AssetStateStale
		default:
			change.State = AssetStateLocallyModified
		}
		change.Diff = diffFiles(asset.Asset.TargetPath, actual.Files, actual.Exists && !actual.Unsafe, asset.Files)
		proposal.Changes = append(proposal.Changes, change)
		if change.State == AssetStateConflicting || change.State == AssetStateLocallyModified {
			proposal.RetainedAssets = append(proposal.RetainedAssets, asset.Asset.ID)
		}
	}
	for _, entry := range snapshot.Provenance.Assets {
		if _, exists := desired[entry.AssetID]; !exists {
			proposal.RetainedAssets = append(proposal.RetainedAssets, entry.AssetID)
		}
	}
	sort.Slice(proposal.Changes, func(i, j int) bool { return proposal.Changes[i].AssetID < proposal.Changes[j].AssetID })
	sort.Strings(proposal.RetainedAssets)
	proposal.NextManifest = nextProvenance(catalog, snapshot.Provenance)
	proposal.ManifestDiff = diffYAML(".agents/manifest.yaml", snapshot.ProvenanceContent, proposal.NextManifest)
	proposal.Diff = proposalDiff(proposal)
	proposal.Fingerprint = proposalFingerprint(proposal)
	return proposal, nil
}

func nextProvenance(catalog Catalog, previous DistributionManifest) DistributionManifest {
	byID := make(map[string]ProvenanceEntry, len(previous.Assets)+len(catalog.Assets))
	for _, entry := range previous.Assets {
		sort.Slice(entry.Files, func(i, j int) bool { return entry.Files[i].Path < entry.Files[j].Path })
		byID[entry.AssetID] = entry
	}
	for _, asset := range catalog.Assets {
		if asset.Asset.Kind == AssetKindGlobalPolicy {
			continue
		}
		files := make([]FileChecksum, 0, len(asset.Files))
		for _, file := range sortedKeys(asset.Files) {
			files = append(files, FileChecksum{Path: file, Checksum: ChecksumFiles(map[string][]byte{file: asset.Files[file]})})
		}
		byID[asset.Asset.ID] = ProvenanceEntry{
			AssetID: asset.Asset.ID, Kind: asset.Asset.Kind, SourcePath: asset.Asset.SourcePath,
			Ownership: OwnershipManaged, Version: asset.Asset.Version, TargetPath: asset.Asset.TargetPath,
			Checksum: asset.Checksum, Files: files,
		}
	}
	entries := make([]ProvenanceEntry, 0, len(byID))
	for _, entry := range byID {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].AssetID < entries[j].AssetID })
	return DistributionManifest{SchemaVersion: ManifestSchemaVersion, Ownership: OwnershipGenerated, Generator: "course-dev-orchestrator/agentcontrol-v1", Assets: entries}
}

func ValidateDistributionManifest(manifest DistributionManifest, exists bool) error {
	if !exists {
		return nil
	}
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.Ownership != OwnershipGenerated || manifest.Generator == "" {
		return errors.New("existing .agents/manifest.yaml has unsupported generated-provenance metadata")
	}
	seen := map[string]struct{}{}
	for _, entry := range manifest.Assets {
		if entry.AssetID == "" || entry.Version == "" || entry.Ownership != OwnershipManaged && entry.Ownership != OwnershipLocal {
			return fmt.Errorf("invalid provenance entry %q", entry.AssetID)
		}
		if _, duplicate := seen[entry.AssetID]; duplicate {
			return fmt.Errorf("duplicate provenance asset id %q", entry.AssetID)
		}
		seen[entry.AssetID] = struct{}{}
		if !strings.HasPrefix(entry.TargetPath, ".agents/") || validateRelativePath(entry.TargetPath) != nil {
			return fmt.Errorf("provenance target %q is outside .agents/**", entry.TargetPath)
		}
		if !isSHA256(entry.Checksum) || len(entry.Files) == 0 {
			return fmt.Errorf("provenance entry %q lacks checksums", entry.AssetID)
		}
		fileSeen := map[string]struct{}{}
		for _, file := range entry.Files {
			if err := validateRelativePath(file.Path); err != nil || !isSHA256(file.Checksum) {
				return fmt.Errorf("provenance entry %q contains invalid file checksum", entry.AssetID)
			}
			if _, duplicate := fileSeen[file.Path]; duplicate {
				return fmt.Errorf("provenance entry %q has duplicate file %q", entry.AssetID, file.Path)
			}
			fileSeen[file.Path] = struct{}{}
		}
	}
	return nil
}

func isSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func matchesProvenance(files map[string][]byte, prior ProvenanceEntry) bool {
	if len(files) != len(prior.Files) {
		return false
	}
	for _, expected := range prior.Files {
		content, ok := files[expected.Path]
		if !ok || ChecksumFiles(map[string][]byte{expected.Path: content}) != expected.Checksum {
			return false
		}
	}
	return ChecksumFiles(files) == prior.Checksum
}

func proposalFingerprint(proposal Proposal) string {
	material := struct {
		SchemaVersion  int    `json:"schema_version"`
		RepositoryRoot string `json:"repository_root"`
		BaseRevision   string `json:"base_revision"`
		CatalogDigest  string `json:"catalog_digest"`
		Changes        []struct {
			AssetID          string     `json:"asset_id"`
			TargetPath       string     `json:"target_path"`
			State            AssetState `json:"state"`
			CurrentChecksum  string     `json:"current_checksum,omitempty"`
			PreviousChecksum string     `json:"previous_checksum,omitempty"`
			DesiredChecksum  string     `json:"desired_checksum"`
		} `json:"changes"`
		RetainedAssets []string             `json:"retained_assets,omitempty"`
		NextManifest   DistributionManifest `json:"next_manifest"`
		ManifestDiff   string               `json:"manifest_diff,omitempty"`
	}{SchemaVersion: proposal.SchemaVersion, RepositoryRoot: proposal.RepositoryRoot, BaseRevision: proposal.BaseRevision, CatalogDigest: proposal.CatalogDigest, RetainedAssets: proposal.RetainedAssets, NextManifest: proposal.NextManifest, ManifestDiff: proposal.ManifestDiff}
	for _, change := range proposal.Changes {
		material.Changes = append(material.Changes, struct {
			AssetID          string     `json:"asset_id"`
			TargetPath       string     `json:"target_path"`
			State            AssetState `json:"state"`
			CurrentChecksum  string     `json:"current_checksum,omitempty"`
			PreviousChecksum string     `json:"previous_checksum,omitempty"`
			DesiredChecksum  string     `json:"desired_checksum"`
		}{change.AssetID, change.TargetPath, change.State, change.CurrentChecksum, change.PreviousChecksum, change.DesiredChecksum})
	}
	encoded, _ := json.Marshal(material)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func proposalDiff(proposal Proposal) string {
	var output strings.Builder
	for _, change := range proposal.Changes {
		if change.State == AssetStateCurrent && change.Diff == "" {
			continue
		}
		fmt.Fprintf(&output, "# %s: %s\n", change.AssetID, change.State)
		output.WriteString(change.Diff)
	}
	if proposal.ManifestDiff != "" {
		output.WriteString("# generated managed-asset provenance\n")
		output.WriteString(proposal.ManifestDiff)
	}
	return output.String()
}

func diffFiles(targetPath string, current map[string][]byte, exists bool, desired map[string][]byte) string {
	var output strings.Builder
	keys := map[string]struct{}{}
	for name := range current {
		keys[name] = struct{}{}
	}
	for name := range desired {
		keys[name] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for name := range keys {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		before, hasBefore := current[name]
		after, hasAfter := desired[name]
		if hasBefore && hasAfter && string(before) == string(after) {
			continue
		}
		filePath := targetPath
		if len(desired) > 1 || path.Ext(targetPath) == "" {
			filePath = path.Join(targetPath, name)
		}
		from := []string{}
		if hasBefore && exists {
			from = diffLines(before)
		}
		to := []string{}
		if hasAfter {
			to = diffLines(after)
		}
		text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A: from, B: to, FromFile: "a/" + filePath, ToFile: "b/" + filePath,
			Context: 3,
		})
		if err == nil {
			output.WriteString(text)
		}
	}
	return output.String()
}

func diffLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(string(content), "\n")
	if strings.HasSuffix(string(content), "\n") {
		return lines[:len(lines)-1]
	}
	return append(lines, "\\ No newline at end of file")
}

func diffYAML(name string, before []byte, after any) string {
	encoded, err := yaml.Marshal(after)
	if err != nil || string(before) == string(encoded) {
		return ""
	}
	return diffFiles(name, map[string][]byte{path.Base(name): before}, len(before) != 0, map[string][]byte{path.Base(name): encoded})
}

func MarshalDistributionManifest(manifest DistributionManifest) ([]byte, error) {
	if err := ValidateDistributionManifest(manifest, true); err != nil {
		return nil, err
	}
	return yaml.Marshal(manifest)
}
