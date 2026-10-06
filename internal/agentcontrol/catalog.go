package agentcontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	checksumComputed = "computed"
	maxManifestBytes = int64(1 << 20)
	maxAssetBytes    = int64(4 << 20)
	maxCatalogBytes  = int64(16 << 20)
)

func LoadCatalog(source fs.FS) (Catalog, error) {
	manifestInfo, err := fs.Stat(source, "agent-system/manifest.yaml")
	if err != nil {
		return Catalog{}, fmt.Errorf("inspect agent-system manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > maxManifestBytes {
		return Catalog{}, fmt.Errorf("agent-system manifest must be a regular file no larger than %d bytes", maxManifestBytes)
	}
	manifestBytes, err := fs.ReadFile(source, "agent-system/manifest.yaml")
	if err != nil {
		return Catalog{}, fmt.Errorf("read agent-system manifest: %w", err)
	}
	var manifest Manifest
	if err := DecodeStrictYAML(manifestBytes, &manifest); err != nil {
		return Catalog{}, fmt.Errorf("decode agent-system manifest: %w", err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return Catalog{}, fmt.Errorf("unsupported agent-system manifest schema %d", manifest.SchemaVersion)
	}
	catalog := Catalog{Manifest: manifest, Profiles: map[string]Profile{}}
	var totalBytes int64
	for index := range manifest.Assets {
		asset := manifest.Assets[index]
		files, err := readAssetFiles(source, asset.SourcePath)
		if err != nil {
			return Catalog{}, fmt.Errorf("read asset %q: %w", asset.ID, err)
		}
		var size int64
		for _, content := range files {
			size += int64(len(content))
		}
		if size > maxAssetBytes {
			return Catalog{}, fmt.Errorf("asset %q exceeds %d bytes", asset.ID, maxAssetBytes)
		}
		totalBytes += size
		if totalBytes > maxCatalogBytes {
			return Catalog{}, fmt.Errorf("agent-system assets exceed %d bytes", maxCatalogBytes)
		}
		checksum := ChecksumFiles(files)
		if asset.Checksum != checksumComputed && asset.Checksum != checksum {
			return Catalog{}, fmt.Errorf("asset %q checksum mismatch: manifest=%q computed=%q", asset.ID, asset.Checksum, checksum)
		}
		asset.Checksum = checksum
		manifest.Assets[index].Checksum = checksum
		resolved := ResolvedAsset{Asset: asset, Files: files, Checksum: checksum}
		catalog.Assets = append(catalog.Assets, resolved)
		if asset.Kind == AssetKindProfile {
			if len(files) != 1 {
				return Catalog{}, fmt.Errorf("profile asset %q must contain exactly one YAML file", asset.ID)
			}
			for _, content := range files {
				var profile Profile
				if err := DecodeStrictYAML(content, &profile); err != nil {
					return Catalog{}, fmt.Errorf("decode profile asset %q: %w", asset.ID, err)
				}
				if profile.ID != strings.TrimPrefix(asset.ID, "profile/") {
					return Catalog{}, fmt.Errorf("profile asset %q declares profile id %q", asset.ID, profile.ID)
				}
				if _, duplicate := catalog.Profiles[profile.ID]; duplicate {
					return Catalog{}, fmt.Errorf("duplicate profile id %q", profile.ID)
				}
				catalog.Profiles[profile.ID] = profile
			}
		}
	}
	catalog.Manifest = manifest
	if err := ValidateCatalog(catalog); err != nil {
		return Catalog{}, err
	}
	catalog.Digest = catalogDigest(catalog.Assets)
	return catalog, nil
}

func ValidateCatalog(catalog Catalog) error {
	if catalog.Manifest.SchemaVersion != 0 && catalog.Manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported agent-system manifest schema %d", catalog.Manifest.SchemaVersion)
	}
	ids := map[string]struct{}{}
	targets := map[string]string{}
	directoryTargets := []string{}
	profiles := map[string]struct{}{}
	for _, profile := range catalog.Profiles {
		if err := ValidateProfile(profile); err != nil {
			return err
		}
		profiles[profile.ID] = struct{}{}
	}
	for _, resolved := range catalog.Assets {
		asset := resolved.Asset
		if strings.TrimSpace(asset.ID) == "" || strings.TrimSpace(asset.Version) == "" {
			return fmt.Errorf("managed asset requires stable id and version")
		}
		if _, duplicate := ids[asset.ID]; duplicate {
			return fmt.Errorf("duplicate asset id %q", asset.ID)
		}
		ids[asset.ID] = struct{}{}
		if asset.Ownership != OwnershipManaged {
			return fmt.Errorf("canonical asset %q must be MANAGED", asset.ID)
		}
		if !strings.HasPrefix(asset.SourcePath, "agent-system/") || strings.HasPrefix(asset.SourcePath, "agent-system/.ai/") {
			return fmt.Errorf("asset %q source must be inside agent-system and cannot be .ai knowledge", asset.ID)
		}
		if err := validateRelativePath(asset.SourcePath); err != nil {
			return fmt.Errorf("asset %q source path: %w", asset.ID, err)
		}
		if len(resolved.Files) == 0 {
			return fmt.Errorf("asset %q has no source files", asset.ID)
		}
		for name := range resolved.Files {
			if err := validateRelativePath(name); err != nil {
				return fmt.Errorf("asset %q contains unsafe file path: %w", asset.ID, err)
			}
		}
		if !isSHA256(resolved.Checksum) {
			return fmt.Errorf("asset %q has no computed sha256 checksum", asset.ID)
		}
		if asset.Kind == AssetKindGlobalPolicy {
			if asset.ID != "global-policy" || asset.TargetPath != "~/.codex/AGENTS.md" {
				return fmt.Errorf("global policy asset must use stable id global-policy and target ~/.codex/AGENTS.md")
			}
		} else {
			if !strings.HasPrefix(asset.TargetPath, ".agents/") {
				return fmt.Errorf("repository asset %q target must be under .agents/**", asset.ID)
			}
			if err := validateRelativePath(asset.TargetPath); err != nil {
				return fmt.Errorf("asset %q target path: %w", asset.ID, err)
			}
			if previous, exists := targets[asset.TargetPath]; exists {
				return fmt.Errorf("assets %q and %q share target %q", previous, asset.ID, asset.TargetPath)
			}
			targets[asset.TargetPath] = asset.ID
			if asset.Kind == AssetKindSkill {
				directoryTargets = append(directoryTargets, asset.TargetPath)
			}
		}
		switch asset.Kind {
		case AssetKindGlobalPolicy:
			if _, ok := resolved.Files["AGENTS.md"]; !ok {
				return fmt.Errorf("global policy asset must contain AGENTS.md")
			}
		case AssetKindSkill:
			if !strings.HasPrefix(asset.ID, "skill/") || !strings.HasSuffix(asset.TargetPath, strings.TrimPrefix(asset.ID, "skill/")) {
				return fmt.Errorf("skill asset %q must have stable skill/<name> id and matching target", asset.ID)
			}
			if content, ok := resolved.Files["SKILL.md"]; !ok || len(bytes.TrimSpace(content)) == 0 {
				return fmt.Errorf("skill asset %q must contain non-empty SKILL.md", asset.ID)
			}
		case AssetKindProfile:
			if !strings.HasPrefix(asset.ID, "profile/") || !strings.HasPrefix(asset.TargetPath, ".agents/profiles/") {
				return fmt.Errorf("profile asset %q must use profile/<id> and .agents/profiles target", asset.ID)
			}
			if path.Base(asset.SourcePath) != path.Base(asset.TargetPath) {
				return fmt.Errorf("profile asset %q source and target filenames must match", asset.ID)
			}
		case "":
			return fmt.Errorf("asset %q has empty kind", asset.ID)
		default:
			return fmt.Errorf("asset %q has unsupported kind %q", asset.ID, asset.Kind)
		}
		for _, profileID := range asset.Profiles {
			if profileID == "*" {
				continue
			}
			if _, ok := profiles[profileID]; !ok {
				return fmt.Errorf("asset %q references unknown profile %q", asset.ID, profileID)
			}
		}
	}
	for directory := range directoryTargets {
		for target := range targets {
			if target != directoryTargets[directory] && strings.HasPrefix(target, directoryTargets[directory]+"/") {
				return fmt.Errorf("repository asset target %q overlaps managed skill directory %q", target, directoryTargets[directory])
			}
		}
	}
	for _, requiredID := range []string{"global-policy", "skill/task-route", "skill/contract-plan", "profile/go.canonical", "profile/nextjs.common"} {
		if _, ok := ids[requiredID]; !ok {
			return fmt.Errorf("canonical manifest is missing required asset %q", requiredID)
		}
	}
	for _, requiredProfile := range []string{"go.canonical", "nextjs.common"} {
		if _, ok := profiles[requiredProfile]; !ok {
			return fmt.Errorf("canonical manifest is missing required profile %q", requiredProfile)
		}
	}
	return nil
}

func ChecksumFiles(files map[string][]byte) string {
	digest := sha256.New()
	for _, name := range sortedKeys(files) {
		_ = binary.Write(digest, binary.BigEndian, uint64(len(name)))
		_, _ = digest.Write([]byte(name))
		_ = binary.Write(digest, binary.BigEndian, uint64(len(files[name])))
		_, _ = digest.Write(files[name])
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func catalogDigest(assets []ResolvedAsset) string {
	ordered := append([]ResolvedAsset(nil), assets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Asset.ID < ordered[j].Asset.ID })
	material := make([]struct {
		ID         string    `json:"id"`
		Kind       AssetKind `json:"kind"`
		SourcePath string    `json:"source_path"`
		Ownership  Ownership `json:"ownership"`
		Version    string    `json:"version"`
		TargetPath string    `json:"target_path"`
		Profiles   []string  `json:"profiles"`
		Checksum   string    `json:"checksum"`
	}, 0, len(ordered))
	for _, asset := range ordered {
		profiles := append([]string(nil), asset.Asset.Profiles...)
		sort.Strings(profiles)
		material = append(material, struct {
			ID         string    `json:"id"`
			Kind       AssetKind `json:"kind"`
			SourcePath string    `json:"source_path"`
			Ownership  Ownership `json:"ownership"`
			Version    string    `json:"version"`
			TargetPath string    `json:"target_path"`
			Profiles   []string  `json:"profiles"`
			Checksum   string    `json:"checksum"`
		}{asset.Asset.ID, asset.Asset.Kind, asset.Asset.SourcePath, asset.Asset.Ownership, asset.Asset.Version, asset.Asset.TargetPath, profiles, asset.Checksum})
	}
	encoded, _ := json.Marshal(material)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func readAssetFiles(source fs.FS, sourcePath string) (map[string][]byte, error) {
	if err := validateRelativePath(sourcePath); err != nil {
		return nil, err
	}
	info, err := fs.Stat(source, sourcePath)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	if !info.IsDir() {
		if !info.Mode().IsRegular() || info.Size() > maxAssetBytes {
			return nil, fmt.Errorf("source is not a regular file within size limit")
		}
		content, err := fs.ReadFile(source, sourcePath)
		if err != nil {
			return nil, err
		}
		files[path.Base(sourcePath)] = content
		return files, nil
	}
	err = fs.WalkDir(source, sourcePath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("canonical source contains symlink %q", current)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("canonical source contains non-regular file %q", current)
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if fileInfo.Size() > maxAssetBytes {
			return fmt.Errorf("canonical source file %q exceeds size limit", current)
		}
		content, err := fs.ReadFile(source, current)
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(current, sourcePath), "/")
		if relative == "." {
			return nil
		}
		files[relative] = content
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func DecodeStrictYAML(content []byte, destination any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("multiple YAML documents are not supported")
	}
	return nil
}
