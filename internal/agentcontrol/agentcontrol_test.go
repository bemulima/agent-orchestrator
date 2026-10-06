package agentcontrol

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadCanonicalCatalogDerivesChecksumsAndValidatesProfiles(t *testing.T) {
	catalog, err := LoadCatalog(os.DirFS("../.."))
	require.NoError(t, err)

	assets := map[string]ResolvedAsset{}
	for _, asset := range catalog.Assets {
		assets[asset.Asset.ID] = asset
		require.Equal(t, OwnershipManaged, asset.Asset.Ownership)
		require.True(t, strings.HasPrefix(asset.Checksum, "sha256:"))
	}
	require.Contains(t, assets, "global-policy")
	require.Contains(t, assets, "skill/task-route")
	require.Contains(t, assets, "skill/contract-plan")
	require.Contains(t, assets, "profile/go.canonical")
	require.Contains(t, assets, "profile/nextjs.common")
	require.NotEmpty(t, catalog.Digest)
	require.NoError(t, ValidateCatalog(catalog))
}

func TestChecksumTreeIsDeterministicAndIncludesNames(t *testing.T) {
	first := map[string][]byte{"SKILL.md": []byte("route\n"), "references/example.yaml": []byte("ok: true\n")}
	second := map[string][]byte{"references/example.yaml": []byte("ok: true\n"), "SKILL.md": []byte("route\n")}
	require.Equal(t, ChecksumFiles(first), ChecksumFiles(second))
	require.NotEqual(t, ChecksumFiles(first), ChecksumFiles(map[string][]byte{"SKILL.md": []byte("route\n"), "references/renamed.yaml": []byte("ok: true\n")}))
}

func TestDiffReportsNewlineOnlyChanges(t *testing.T) {
	diff := diffFiles(".agents/skills/example/SKILL.md", map[string][]byte{"SKILL.md": []byte("same\n")}, true,
		map[string][]byte{"SKILL.md": []byte("same")})
	require.Contains(t, diff, "No newline at end of file")
}

func TestValidateProfileRequiresEvidenceAndRequiredRoutes(t *testing.T) {
	profile := Profile{
		SchemaVersion: 1, ID: "go.canonical", Name: "Go", Stack: "go",
		RepoShape:        RepositoryShape{RequiredPaths: []string{"go.mod", "internal/domain/"}},
		ContractLocation: ContractLocation{Language: "go", Extension: ".go", StandardLibraryImports: "allow", ThirdPartyImportPolicy: "deny"},
		Routes: []ProfileRoute{
			{ID: "backend.domain", Purpose: "domain invariants", Targets: []string{"internal/domain/**"}, ContractSurface: &ContractSurface{Directory: "internal/domain", PackageName: "domain"}, RequireEvidence: true, VerificationKinds: []string{"domain-unit"}},
		}}
	err := ValidateProfile(profile)
	require.Error(t, err)
	require.ErrorContains(t, err, "backend.usecase")

	profile.Routes = append(profile.Routes, ProfileRoute{
		ID: "backend.usecase", Purpose: "business process", Targets: []string{"internal/usecase/**"},
		ContractSurface:     &ContractSurface{Directory: "internal/usecase", PackageName: "usecase"},
		AllowedDependencies: []string{"backend.domain"}, RequireEvidence: true, VerificationKinds: []string{"usecase-unit"},
	})
	require.ErrorContains(t, ValidateProfile(profile), "missing required route")
}

func TestClassifyOwnershipKeepsLocalSkillsAndRepositoryFactsLocal(t *testing.T) {
	provenance := DistributionManifest{Assets: []ProvenanceEntry{{
		AssetID: "skill/task-route", Ownership: OwnershipManaged, TargetPath: ".agents/skills/task-route",
	}}}
	require.Equal(t, OwnershipLocal, ClassifyOwnership("AGENTS.md", provenance))
	require.Equal(t, OwnershipLocal, ClassifyOwnership(".ai/service.yaml", provenance))
	require.Equal(t, OwnershipGenerated, ClassifyOwnership(".ai/architecture/service.mmd", provenance))
	require.Equal(t, OwnershipGenerated, ClassifyOwnership(".agents/manifest.yaml", provenance))
	require.Equal(t, OwnershipManaged, ClassifyOwnership(".agents/skills/task-route/SKILL.md", provenance))
	require.Equal(t, OwnershipLocal, ClassifyOwnership(".agents/skills/local-lint/SKILL.md", provenance))
}

func TestContractBlockerCodesAreValidated(t *testing.T) {
	for _, code := range []BlockerCode{
		BlockerContractChangeRequired,
		BlockerOutOfScopeChangeRequired,
		BlockerDependencyNotReady,
		BlockerArchitectureConflict,
		BlockerTestBoundaryMissing,
		BlockerBaselineStale,
	} {
		require.NoError(t, ValidateContractBlocker(ContractBlocker{Code: code, Evidence: "source evidence"}))
	}
	require.Error(t, ValidateContractBlocker(ContractBlocker{Code: "invented", Evidence: "source evidence"}))
}

func TestDistributionProposalClassifiesAndDoesNotClaimLocalSkills(t *testing.T) {
	catalog := testCatalog("shared v1\n")
	oldFiles := map[string][]byte{"go-canonical.yaml": []byte("previous managed profile\n")}
	snapshot := RepositorySnapshot{
		Root: "/repo", Revision: "base-1",
		Assets: map[string]TargetAsset{
			"skill/task-route":     {Exists: false},
			"profile/go.canonical": {Exists: true, Files: map[string][]byte{"go-canonical.yaml": []byte("local edit\n")}},
		},
		HasProvenance: true,
		Provenance: DistributionManifest{SchemaVersion: 1, Ownership: OwnershipGenerated, Generator: "test", Assets: []ProvenanceEntry{
			{AssetID: "profile/go.canonical", Kind: AssetKindProfile, SourcePath: "agent-system/profiles/go-canonical.yaml", Ownership: OwnershipManaged, Version: "1.0.0", TargetPath: ".agents/profiles/go-canonical.yaml", Checksum: ChecksumFiles(oldFiles), Files: []FileChecksum{{Path: "go-canonical.yaml", Checksum: ChecksumFiles(oldFiles)}}},
		}},
	}
	proposal, err := BuildDistributionProposal(catalog, snapshot)
	require.NoError(t, err)
	require.NotEmpty(t, proposal.Fingerprint)
	require.Equal(t, AssetStateMissing, proposal.Change("skill/task-route").State)
	require.Equal(t, AssetStateLocallyModified, proposal.Change("profile/go.canonical").State)
	require.Contains(t, proposal.RetainedAssets, "profile/go.canonical")
}

func TestDistributionFingerprintBindsCarriedProvenance(t *testing.T) {
	localFiles := map[string][]byte{"SKILL.md": []byte("repository-owned\n")}
	entry := ProvenanceEntry{
		AssetID: "local-skill/notes", Kind: AssetKindSkill, SourcePath: "local",
		Ownership: OwnershipLocal, Version: "1.0.0", TargetPath: ".agents/skills/notes",
		Checksum: ChecksumFiles(localFiles), Files: []FileChecksum{{Path: "SKILL.md", Checksum: ChecksumFiles(localFiles)}},
	}
	makeSnapshot := func(version string) RepositorySnapshot {
		carried := entry
		carried.Version = version
		return RepositorySnapshot{
			Root: "/repo", Revision: "base-1", Assets: map[string]TargetAsset{}, HasProvenance: true,
			Provenance: DistributionManifest{SchemaVersion: 1, Ownership: OwnershipGenerated, Generator: "test", Assets: []ProvenanceEntry{carried}},
		}
	}
	first, err := BuildDistributionProposal(testCatalog("shared v1\n"), makeSnapshot("1.0.0"))
	require.NoError(t, err)
	second, err := BuildDistributionProposal(testCatalog("shared v1\n"), makeSnapshot("2.0.0"))
	require.NoError(t, err)
	require.NotEqual(t, first.Fingerprint, second.Fingerprint)
}

func TestGlobalPolicyPlanIsReadOnlyAndReportsDrift(t *testing.T) {
	catalog := testCatalog("global policy\n")
	missing, err := BuildGlobalPolicyProposal(catalog, "/fake-home/.codex/AGENTS.md", nil, false)
	require.NoError(t, err)
	require.Equal(t, GlobalPolicyMissing, missing.State)
	require.NotEmpty(t, missing.Diff)

	modified, err := BuildGlobalPolicyProposal(catalog, "/fake-home/.codex/AGENTS.md", []byte("user policy\n"), true)
	require.NoError(t, err)
	require.Equal(t, GlobalPolicyDiffers, modified.State)
	require.Contains(t, modified.Diff, "user policy")
	require.EqualError(t, ApplyGlobalPolicy(context.Background(), modified, modified.Fingerprint), ErrGlobalPolicyInstallUnavailable.Error())
}

func testCatalog(skillContent string) Catalog {
	global := []byte("global policy\n")
	catalog := Catalog{
		Assets: []ResolvedAsset{
			{Asset: Asset{ID: "skill/task-route", Kind: AssetKindSkill, SourcePath: "agent-system/skills/task-route", Ownership: OwnershipManaged, Version: "1.0.0", TargetPath: ".agents/skills/task-route", Profiles: []string{"go.canonical", "nextjs.common"}}, Files: map[string][]byte{"SKILL.md": []byte(skillContent)}, Checksum: ChecksumFiles(map[string][]byte{"SKILL.md": []byte(skillContent)})},
			{Asset: Asset{ID: "profile/go.canonical", Kind: AssetKindProfile, SourcePath: "agent-system/profiles/go-canonical.yaml", Ownership: OwnershipManaged, Version: "1.0.0", TargetPath: ".agents/profiles/go-canonical.yaml", Profiles: []string{"go.canonical"}}, Files: map[string][]byte{"go-canonical.yaml": []byte("profile\n")}, Checksum: ChecksumFiles(map[string][]byte{"go-canonical.yaml": []byte("profile\n")})},
			{Asset: Asset{ID: "global-policy", Kind: AssetKindGlobalPolicy, SourcePath: "agent-system/global/AGENTS.md", Ownership: OwnershipManaged, Version: "1.0.0", TargetPath: "~/.codex/AGENTS.md", Profiles: []string{"*"}}, Files: map[string][]byte{"AGENTS.md": global}, Checksum: ChecksumFiles(map[string][]byte{"AGENTS.md": global})},
		},
	}
	catalog.Digest = catalogDigest(catalog.Assets)
	return catalog
}
