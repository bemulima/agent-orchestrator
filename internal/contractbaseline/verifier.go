package contractbaseline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

var secretLikeContent = regexp.MustCompile(`(?i)(-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\b(?:api[_-]?key|access[_-]?token|password|secret)\s*[:=]\s*["'][^"']{8,}["'])`)

func ContainsSecretLikeContent(content []byte) bool { return secretLikeContent.Match(content) }

type ContractDiffReport struct {
	Passed       bool                          `json:"passed"`
	ChangedFiles []string                      `json:"changed_files"`
	FileHashes   []domain.ContractBaselineFile `json:"file_hashes"`
	DiffSHA256   string                        `json:"diff_sha256"`
	Reasons      []string                      `json:"reasons,omitempty"`
}

// VerifyContractDiff checks every changed path and its complete candidate content.
// Only files selected by the approved ContractPlan may differ from the source base.
func VerifyContractDiff(
	ctx context.Context,
	root, baseRevision string,
	profile agentcontrol.Profile,
	files []domain.ContractBaselineFile,
	contracts []domain.ContractReference,
	changedPaths []string,
	diff string,
	readFile func(context.Context, string) ([]byte, error),
) (ContractDiffReport, error) {
	report := ContractDiffReport{Passed: true, ChangedFiles: append([]string(nil), changedPaths...), Reasons: []string{}}
	sort.Strings(report.ChangedFiles)
	if len(report.ChangedFiles) == 0 {
		addDiffReason(&report, "Contract Agent produced no changed contract files")
	}
	if _, err := gitOutput(ctx, root, "rev-parse", "--verify", baseRevision+"^{commit}"); err != nil {
		addDiffReason(&report, "approved source revision is not a Git commit")
	}
	allowedPaths := make(map[string]domain.ContractBaselineFile, len(files))
	for _, file := range files {
		if err := agentcontrol.ValidateRelativePath(file.Path); err != nil || secretLikePath(file.Path) || !contractLocationPath(file.Path, profile) {
			addDiffReason(&report, "approved contract path is unsafe: "+file.Path)
			continue
		}
		allowedPaths[file.Path] = file
	}
	targets := map[string]map[string]struct{}{}
	for _, contract := range contracts {
		if _, ok := allowedPaths[contract.Path]; !ok || !agentcontrol.ContractPathOwnedByRoute(profile, contract.RouteID, contract.Path) {
			addDiffReason(&report, "contract reference is outside the approved file set: "+contract.Path)
			continue
		}
		if strings.TrimSpace(contract.Symbol) == "" {
			addDiffReason(&report, "contract reference is outside the approved file set: "+contract.Path)
			continue
		}
		if targets[contract.Path] == nil {
			targets[contract.Path] = map[string]struct{}{}
		}
		targets[contract.Path][contract.Symbol] = struct{}{}
	}
	if len(targets) != len(allowedPaths) {
		for relative := range allowedPaths {
			if len(targets[relative]) == 0 {
				addDiffReason(&report, "approved contract file has no referenced boundary: "+relative)
			}
		}
	}
	seen := map[string]struct{}{}
	for _, relative := range report.ChangedFiles {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if _, duplicate := seen[relative]; duplicate {
			addDiffReason(&report, "duplicate changed path: "+relative)
			continue
		}
		seen[relative] = struct{}{}
		if _, allowed := allowedPaths[relative]; !allowed {
			addDiffReason(&report, "unexpected file in full Git diff: "+relative)
			continue
		}
		if secretLikePath(relative) {
			addDiffReason(&report, "secret-like file is not allowed in a contract diff: "+relative)
			continue
		}
		content, err := readFile(ctx, relative)
		if err != nil {
			addDiffReason(&report, "cannot read changed file "+relative+": "+err.Error())
			continue
		}
		if len(content) > 256<<10 {
			addDiffReason(&report, "contract file exceeds the size limit: "+relative)
			continue
		}
		if secretLikeContent.Match(content) {
			addDiffReason(&report, "secret-like content is not allowed in a contract diff: "+relative)
			continue
		}
		baseContent, baseErr := gitFileAt(ctx, root, baseRevision, relative)
		existed := baseErr == nil
		if profile.ContractLocation.Language == "go" {
			if err := verifyGoContractFile(ctx, root, relative, content, baseContent, existed, profile, refsForPath(contracts, relative)); err != nil {
				addDiffReason(&report, relative+": "+err.Error())
			}
		} else if profile.ContractLocation.Language == "typescript" {
			if err := verifyTypeScriptContractFile(relative, content, baseContent, existed, profile, targets[relative]); err != nil {
				addDiffReason(&report, relative+": "+err.Error())
			}
		} else {
			addDiffReason(&report, "unsupported contract language "+profile.ContractLocation.Language)
		}
		report.FileHashes = append(report.FileHashes, domain.ContractBaselineFile{Path: relative, SHA256: contentHash(content), Generated: allowedPaths[relative].Generated})
	}
	for relative, symbols := range targets {
		content, readErr := readFile(ctx, relative)
		if readErr != nil {
			addDiffReason(&report, "approved contract file is missing: "+relative)
			continue
		}
		if len(symbols) == 0 {
			addDiffReason(&report, "approved contract file has no declared boundary symbol: "+relative)
		}
		if _, alreadyVerified := seen[relative]; alreadyVerified {
			continue
		}
		baseContent, baseErr := gitFileAt(ctx, root, baseRevision, relative)
		if profile.ContractLocation.Language == "go" {
			if err := verifyGoContractFile(ctx, root, relative, content, baseContent, baseErr == nil, profile, refsForPath(contracts, relative)); err != nil {
				addDiffReason(&report, relative+": "+err.Error())
			}
		} else if profile.ContractLocation.Language == "typescript" {
			if err := verifyTypeScriptContractFile(relative, content, baseContent, baseErr == nil, profile, symbols); err != nil {
				addDiffReason(&report, relative+": "+err.Error())
			}
		}
		report.FileHashes = append(report.FileHashes, domain.ContractBaselineFile{
			Path: relative, SHA256: contentHash(content), Generated: allowedPaths[relative].Generated,
		})
	}
	sort.Slice(report.FileHashes, func(i, j int) bool { return report.FileHashes[i].Path < report.FileHashes[j].Path })
	var digest bytes.Buffer
	digest.WriteString(diff)
	for _, file := range report.FileHashes {
		digest.WriteString(file.Path)
		digest.WriteByte(0)
		digest.WriteString(file.SHA256)
		digest.WriteByte('\n')
	}
	hash := sha256.Sum256(digest.Bytes())
	report.DiffSHA256 = hex.EncodeToString(hash[:])
	return report, nil
}

// VerifyCommittedBaseline proves that the local commit is a single contract-only
// child of the approved source base and that persisted hashes describe its tree.
func VerifyCommittedBaseline(
	ctx context.Context,
	root, baseRevision, commit string,
	profile agentcontrol.Profile,
	files []domain.ContractBaselineFile,
	contracts []domain.ContractReference,
) domain.ContractBaselineValidation {
	validation := domain.ContractBaselineValidation{Passed: true, Reasons: []string{}}
	if strings.TrimSpace(commit) == "" || strings.TrimSpace(baseRevision) == "" {
		return domain.ContractBaselineValidation{Passed: false, Reasons: []string{"contract commit or source revision is missing"}}
	}
	if !fullSHA(commit) {
		return domain.ContractBaselineValidation{Passed: false, Reasons: []string{"contract baseline revision is not a full Git SHA"}}
	}
	parents, err := gitOutput(ctx, root, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return domain.ContractBaselineValidation{Passed: false, InspectionFailed: true, Reasons: []string{"cannot inspect contract baseline parent: " + err.Error()}}
	}
	if len(strings.Fields(parents)) != 2 {
		return domain.ContractBaselineValidation{Passed: false, Reasons: []string{"contract baseline must be a non-merge commit with one parent"}}
	}
	parent, err := gitOutput(ctx, root, "rev-parse", commit+"^")
	if err != nil {
		return domain.ContractBaselineValidation{Passed: false, InspectionFailed: true, Reasons: []string{"cannot inspect contract baseline parent revision: " + err.Error()}}
	}
	if strings.TrimSpace(parent) != baseRevision {
		validation.Passed = false
		validation.Reasons = append(validation.Reasons, "contract commit is not a direct child of the approved source revision")
		return validation
	}
	changed, err := commitChangedPaths(ctx, root, commit)
	if err != nil {
		validation.Passed = false
		validation.InspectionFailed = true
		validation.Reasons = append(validation.Reasons, "cannot inspect contract commit diff: "+err.Error())
		return validation
	}
	allowed := map[string]struct{}{}
	filesByPath := map[string]domain.ContractBaselineFile{}
	for _, file := range files {
		if _, duplicate := filesByPath[file.Path]; duplicate {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted contract file path is duplicated: "+file.Path)
		}
		if err := agentcontrol.ValidateRelativePath(file.Path); err != nil || !contractLocationPath(file.Path, profile) || secretLikePath(file.Path) {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted contract file path is outside the profile-owned contract location: "+file.Path)
		}
		filesByPath[file.Path] = file
		allowed[file.Path] = struct{}{}
	}
	if len(changed) == 0 {
		validation.Passed = false
		validation.Reasons = append(validation.Reasons, "contract-only commit has no file changes")
	}
	for _, relative := range changed {
		if _, ok := allowed[relative]; !ok {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "contract commit contains an unapproved path: "+relative)
		}
	}
	contractByPath := map[string][]domain.ContractReference{}
	for _, contract := range contracts {
		if strings.TrimSpace(contract.Symbol) == "" {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted contract reference has no boundary symbol: "+contract.Path)
		}
		if _, exists := filesByPath[contract.Path]; !exists {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted contract reference has no committed file: "+contract.Path)
		}
		if !agentcontrol.ContractPathOwnedByRoute(profile, contract.RouteID, contract.Path) {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted contract reference is outside its owner route surface: "+contract.Path)
		}
		contractByPath[contract.Path] = append(contractByPath[contract.Path], contract)
	}
	for _, file := range files {
		if err := agentcontrol.ValidateRelativePath(file.Path); err != nil || len(contractByPath[file.Path]) == 0 {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted file path is outside approved contract references: "+file.Path)
			continue
		}
		content, readErr := gitFileAt(ctx, root, commit, file.Path)
		if readErr != nil {
			validation.Passed = false
			validation.InspectionFailed = true
			validation.Reasons = append(validation.Reasons, "cannot read contract file from commit: "+file.Path)
			continue
		}
		if contentHash(content) != file.SHA256 {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, "persisted file hash does not match committed content: "+file.Path)
			continue
		}
		baseContent, baseErr := gitFileAt(ctx, root, baseRevision, file.Path)
		if err := validateContractContent(ctx, root, file.Path, content, baseContent, baseErr == nil, profile, contractByPath[file.Path]); err != nil {
			validation.Passed = false
			validation.Reasons = append(validation.Reasons, file.Path+": "+err.Error())
		}
	}
	return validation
}

func validateContractContent(ctx context.Context, root, relative string, content, base []byte, existed bool, profile agentcontrol.Profile, refs []domain.ContractReference) error {
	if !contractLocationPath(relative, profile) {
		return fmt.Errorf("file is outside the profile-owned contract location")
	}
	if secretLikePath(relative) || secretLikeContent.Match(content) {
		return fmt.Errorf("secret-like file or content is not allowed")
	}
	symbols := map[string]struct{}{}
	for _, ref := range refs {
		symbols[ref.Symbol] = struct{}{}
	}
	if profile.ContractLocation.Language == "go" {
		return verifyGoContractFile(ctx, root, relative, content, base, existed, profile, refs)
	}
	if profile.ContractLocation.Language == "typescript" {
		return verifyTypeScriptContractFile(relative, content, base, existed, profile, symbols)
	}
	return fmt.Errorf("unsupported contract language %q", profile.ContractLocation.Language)
}

func verifyGoContractFile(ctx context.Context, root, relative string, content, base []byte, existed bool, profile agentcontrol.Profile, refs []domain.ContractReference) error {
	if path.Ext(relative) != profile.ContractLocation.Extension {
		return fmt.Errorf("file extension is outside the architecture profile")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), relative, content, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return fmt.Errorf("invalid Go syntax: %w", err)
	}
	if len(refs) == 0 {
		return fmt.Errorf("contract file has no approved owner route")
	}
	ownerRoute := refs[0].RouteID
	for _, ref := range refs {
		if ref.RouteID != ownerRoute || !agentcontrol.ContractPathOwnedByRoute(profile, ref.RouteID, relative) {
			return fmt.Errorf("contract references disagree on owner route or path")
		}
	}
	packageName, err := contractPackageName(profile, ownerRoute, relative, existed, base)
	if err != nil {
		return err
	}
	if parsed.Name.Name != packageName {
		return fmt.Errorf("package %q does not match owner package %q", parsed.Name.Name, packageName)
	}
	if err := goImportsAllowed(ctx, root, profile, ownerRoute, parsed); err != nil {
		return err
	}
	for _, comment := range parsed.Comments {
		if strings.Contains(comment.Text(), "//go:") || strings.Contains(comment.Text(), "+build") {
			return fmt.Errorf("build directives are not allowed in contract files")
		}
	}
	currentDecls, err := goContractDeclarations(parsed)
	if err != nil {
		return err
	}
	baseDecls := map[string]string{}
	if existed {
		old, parseErr := parser.ParseFile(token.NewFileSet(), relative, base, parser.AllErrors|parser.ParseComments)
		if parseErr != nil {
			return fmt.Errorf("source-base Go file is invalid: %w", parseErr)
		}
		baseDecls, err = goContractDeclarations(old)
		if err != nil {
			return err
		}
	}
	for key, value := range currentDecls {
		oldValue, existedBefore := baseDecls[key]
		if existedBefore && oldValue == value {
			continue
		}
		if strings.HasPrefix(key, "type:") {
			continue
		}
		if strings.HasPrefix(key, "value:var:") {
			name := strings.TrimPrefix(key, "value:var:")
			if contractValueAllowed(profile, ownerRoute, name) && isErrorSentinel(parsed, name) {
				continue
			}
		}
		return fmt.Errorf("new or modified implementation/value declaration %q is not an approved behavior-free contract value", key)
	}
	for key := range baseDecls {
		if _, existsNow := currentDecls[key]; !existsNow {
			return fmt.Errorf("removed Go declaration %q is outside the approved contract change", key)
		}
	}
	for symbol := range targetSymbols(refs) {
		if _, ok := currentDecls["type:"+symbol]; !ok {
			return fmt.Errorf("approved boundary type %q is missing", symbol)
		}
	}
	for _, ref := range refs {
		if ref.Kind == "repository-port" && !callableGoPort(parsed, ref.Symbol) {
			return fmt.Errorf("repository port %q has no exported callable operation signature", ref.Symbol)
		}
	}
	return nil
}

// Repository ports are APIs used across owner/consumer packages. Marker types
// and empty interfaces cannot support consumer or implementer behavior tests.
func callableGoPort(file *ast.File, symbol string) bool {
	types := map[string]ast.Expr{}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}
		for _, raw := range group.Specs {
			spec := raw.(*ast.TypeSpec)
			types[spec.Name.Name] = spec.Type
		}
	}
	seen := map[string]bool{}
	var visit func(ast.Expr) bool
	visit = func(expression ast.Expr) bool {
		switch value := expression.(type) {
		case *ast.Ident:
			if seen[value.Name] {
				return false
			}
			seen[value.Name] = true
			return visit(types[value.Name])
		case *ast.InterfaceType:
			for _, method := range value.Methods.List {
				if len(method.Names) == 0 && visit(method.Type) {
					return true
				}
				if _, callable := method.Type.(*ast.FuncType); callable {
					for _, name := range method.Names {
						if ast.IsExported(name.Name) {
							return true
						}
					}
				}
			}
		}
		return false
	}
	return visit(&ast.Ident{Name: symbol})
}

func contractValueAllowed(profile agentcontrol.Profile, ownerRouteID, name string) bool {
	surface, ok := agentcontrol.ContractSurfaceForRoute(profile, ownerRouteID)
	if !ok {
		return false
	}
	for _, allowed := range surface.AllowedValues {
		if allowed == name {
			return true
		}
	}
	return false
}

func isErrorSentinel(file *ast.File, name string) bool {
	errorPackage := ""
	for _, imported := range file.Imports {
		if strings.Trim(imported.Path.Value, "\"") != "errors" {
			continue
		}
		errorPackage = "errors"
		if imported.Name != nil {
			errorPackage = imported.Name.Name
		}
	}
	if errorPackage == "" {
		return false
	}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			continue
		}
		for _, raw := range group.Specs {
			value, ok := raw.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != name || value.Type != nil || len(value.Values) != 1 {
				continue
			}
			call, ok := value.Values[0].(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				continue
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "New" {
				continue
			}
			packageName, ok := selector.X.(*ast.Ident)
			if !ok || packageName.Name != errorPackage {
				continue
			}
			if message, ok := call.Args[0].(*ast.BasicLit); ok && message.Kind == token.STRING {
				return true
			}
		}
	}
	return false
}

func refsForPath(refs []domain.ContractReference, relative string) []domain.ContractReference {
	var result []domain.ContractReference
	for _, ref := range refs {
		if ref.Path == relative {
			result = append(result, ref)
		}
	}
	return result
}

func targetSymbols(refs []domain.ContractReference) map[string]struct{} {
	result := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Symbol) != "" {
			result[ref.Symbol] = struct{}{}
		}
	}
	return result
}

func goContractDeclarations(file *ast.File) (map[string]string, error) {
	result := map[string]string{}
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			name := node.Name.Name
			if node.Recv != nil {
				name = "method:" + formatNode(node.Recv) + ":" + name
			}
			result["func:"+name] = formatNode(node)
		case *ast.GenDecl:
			if node.Tok == token.IMPORT {
				continue
			}
			switch node.Tok {
			case token.TYPE:
				for _, raw := range node.Specs {
					spec, ok := raw.(*ast.TypeSpec)
					if !ok {
						return nil, fmt.Errorf("unexpected type declaration node")
					}
					result["type:"+spec.Name.Name] = formatNode(spec)
				}
			case token.CONST, token.VAR:
				for _, raw := range node.Specs {
					spec, ok := raw.(*ast.ValueSpec)
					if !ok {
						return nil, fmt.Errorf("unexpected value declaration node")
					}
					for _, name := range spec.Names {
						result["value:"+node.Tok.String()+":"+name.Name] = formatNode(spec)
					}
				}
			default:
				return nil, fmt.Errorf("unsupported Go declaration %q", node.Tok)
			}
		default:
			return nil, fmt.Errorf("unsupported Go declaration %T", declaration)
		}
	}
	return result, nil
}

func verifyTypeScriptContractFile(relative string, content, base []byte, existed bool, profile agentcontrol.Profile, targets map[string]struct{}) error {
	if path.Ext(relative) != profile.ContractLocation.Extension || len(targets) == 0 {
		return fmt.Errorf("file extension or approved boundary symbols do not match the architecture profile")
	}
	text := string(content)
	if secretLikeContent.Match(content) || strings.Contains(text, "require(") || tsImplementationPattern.MatchString(text) {
		return fmt.Errorf("TypeScript contract contains implementation or secret-like content")
	}
	for _, match := range tsImportPattern.FindAllStringSubmatch(text, -1) {
		if len(match) != 2 || !profileImportAllowed(profile, match[1]) {
			return fmt.Errorf("TypeScript import is outside the architecture profile")
		}
	}
	currentSymbols := tsTopLevelSymbols(text)
	for symbol := range targets {
		if _, ok := currentSymbols[symbol]; !ok {
			return fmt.Errorf("approved TypeScript boundary %q is missing", symbol)
		}
	}
	for symbol := range currentSymbols {
		if _, approved := targets[symbol]; approved {
			continue
		}
		if existed {
			baseSymbols := tsTopLevelSymbols(string(base))
			if _, wasPresent := baseSymbols[symbol]; wasPresent {
				continue
			}
		}
		return fmt.Errorf("unapproved TypeScript type symbol %q", symbol)
	}
	if !hasOnlyTypeScriptDeclarations(text) {
		return fmt.Errorf("TypeScript contract contains a top-level non-type declaration")
	}
	return nil
}

var tsImplementationPattern = regexp.MustCompile(`(?m)\b(?:function|class|const|let|var|new|return|throw|await|async)\b|=>|\brequire\s*\(`)

func tsTopLevelSymbols(text string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, match := range tsTypeDeclarationPattern.FindAllStringSubmatch(text, -1) {
		if len(match) == 2 {
			result[match[1]] = struct{}{}
		}
	}
	return result
}

func hasOnlyTypeScriptDeclarations(text string) bool {
	// This deliberately supports a narrow declaration-only grammar. Go files use
	// go/parser; TS contracts are additionally checked by the repository's npm test.
	text = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`).ReplaceAllString(text, " ")
	depth := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if depth == 0 {
			if !(strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export interface ") ||
				strings.HasPrefix(trimmed, "export type ") || strings.HasPrefix(trimmed, "interface ") ||
				strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "&")) {
				return false
			}
		} else if regexp.MustCompile(`\)\s*\{`).MatchString(trimmed) || strings.Contains(trimmed, "=") {
			return false
		}
		depth += countTSBraces(line)
		if depth < 0 {
			return false
		}
	}
	return depth == 0
}

func countTSBraces(line string) int {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range line {
		if quote != 0 {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		switch r {
		case '{', '[', '(', '<':
			depth++
		case '}', ']', ')', '>':
			depth--
		}
	}
	return depth
}

func gitFileAt(ctx context.Context, root, revision, relative string) ([]byte, error) {
	if err := agentcontrol.ValidateRelativePath(relative); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "git", "-C", root, "show", revision+":"+relative)
	content, err := command.Output()
	if err != nil {
		return nil, err
	}
	return content, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func commitChangedPaths(ctx context.Context, root, commit string) ([]string, error) {
	output, err := gitOutputBytes(ctx, root, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(output, []byte{0})
	var paths []string
	for index := 0; index+1 < len(parts); {
		status := string(parts[index])
		index++
		if status == "" {
			continue
		}
		if status[0] == 'R' || status[0] == 'C' {
			return nil, fmt.Errorf("renames and copies are not allowed in contract commits")
		}
		if index >= len(parts) || status[0] == 'D' {
			return nil, fmt.Errorf("deletions and malformed contract commits are not allowed")
		}
		paths = append(paths, string(parts[index]))
		index++
	}
	return paths, nil
}

func gitOutputBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	return command.Output()
}

func secretLikePath(relative string) bool {
	base := strings.ToLower(path.Base(relative))
	for _, token := range []string{".env", "secret", "credential", ".pem", ".key", ".p12", ".pfx"} {
		if strings.Contains(base, token) {
			return true
		}
	}
	return false
}

func contractLocationPath(relative string, profile agentcontrol.Profile) bool {
	if profile.ContractLocation.Language == "typescript" {
		directory := path.Clean(profile.ContractLocation.Directory)
		if directory == "." || directory == ".." || strings.HasPrefix(directory, "../") || path.IsAbs(directory) {
			return false
		}
		return strings.HasPrefix(path.Clean(relative), directory+"/")
	}
	for _, route := range profile.Routes {
		if agentcontrol.ContractPathOwnedByRoute(profile, route.ID, relative) {
			return true
		}
	}
	return false
}

func addDiffReason(report *ContractDiffReport, reason string) {
	report.Passed = false
	for _, existing := range report.Reasons {
		if existing == reason {
			return
		}
	}
	report.Reasons = append(report.Reasons, reason)
}

func formatNode(node ast.Node) string {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), node); err != nil {
		return fmt.Sprintf("<format-error:%T>", node)
	}
	return buffer.String()
}
