package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	contextadapter "github.com/bemulima/agent-orchestrator/internal/adapters/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/contextretrieval/evaluation"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
	"golang.org/x/sys/unix"
)

const maxContextCommandJSONBytes = 16 << 20

type contextRootFlags map[string]string

func (r *contextRootFlags) String() string { return "explicit source identity=root mappings" }
func (r *contextRootFlags) Set(value string) error {
	id, root, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("root must be source-identity=absolute-path: %w", domain.ErrValidation)
	}
	if *r == nil {
		*r = make(contextRootFlags)
	}
	if _, exists := (*r)[id]; exists {
		return fmt.Errorf("duplicate root identity %q: %w", id, domain.ErrValidation)
	}
	(*r)[id] = root
	return nil
}

type contextCommandFlags struct {
	requestPath, routePath, contractPath, coveragePath, basePath, expandPath, format string
	roots                                                                            contextRootFlags
}

func parseContextCommandFlags(command string, args []string, expand bool) (contextCommandFlags, error) {
	var values contextCommandFlags
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&values.requestPath, "request-json", "", "explicit current retrieval request JSON")
	flags.StringVar(&values.routePath, "route-json", "", "existing RoutingResult JSON")
	flags.StringVar(&values.contractPath, "contract-plan-json", "", "optional existing ContractPlan JSON")
	flags.StringVar(&values.coveragePath, "routing-coverage-json", "", "optional source-bound R1 routing coverage companion JSON")
	flags.StringVar(&values.format, "format", "json", "json or deterministic markdown")
	flags.Var(&values.roots, "root", "repeat source-identity=absolute-root for every admitted source")
	if expand {
		flags.StringVar(&values.basePath, "base-pack-json", "", "base ContextPack JSON; not an admission or policy authority")
		flags.StringVar(&values.expandPath, "expand-json", "", "bounded ExpandRequest JSON")
	}
	if err := flags.Parse(args); err != nil {
		return values, fmt.Errorf("parse %s arguments: %w", command, err)
	}
	if flags.NArg() != 0 || values.requestPath == "" || values.routePath == "" || len(values.roots) == 0 ||
		(expand && (values.basePath == "" || values.expandPath == "")) {
		return values, fmt.Errorf("%s requires --request-json, --route-json and repeated --root identity=absolute-path%s: %w", command,
			contextExpandUsage(expand), domain.ErrValidation)
	}
	if values.format != "json" && values.format != "markdown" {
		return values, fmt.Errorf("format must be json or markdown: %w", domain.ErrValidation)
	}
	return values, nil
}

func contextExpandUsage(expand bool) string {
	if expand {
		return ", --base-pack-json and --expand-json"
	}
	return ""
}

func loadContextCommandRequest(values contextCommandFlags) (contextretrieval.RetrievalRequest, error) {
	var request contextretrieval.RetrievalRequest
	if err := readContextCommandJSON(values.requestPath, &request); err != nil {
		return request, fmt.Errorf("read request JSON: %w", err)
	}
	admitted := make(map[string]contextretrieval.SourceAdmission, len(request.Sources))
	for i := range request.Sources {
		source := &request.Sources[i]
		root, ok := values.roots[source.Identity]
		if !ok || source.Identity == "" {
			return request, fmt.Errorf("each source requires an explicit current root: %w", domain.ErrValidation)
		}
		routeIdentity := source.RouteIdentity
		if routeIdentity == "" {
			routeIdentity = source.Identity
		}
		if _, duplicate := admitted[routeIdentity]; duplicate {
			return request, fmt.Errorf("duplicate admitted source identity: %w", domain.ErrValidation)
		}
		source.Root = root
		admitted[routeIdentity] = *source
	}
	if len(admitted) != len(values.roots) {
		return request, fmt.Errorf("root identity not present in current admission: %w", domain.ErrValidation)
	}
	var route domain.RoutingResult
	if err := readContextCommandJSON(values.routePath, &route); err != nil {
		return request, fmt.Errorf("read route JSON: %w", err)
	}
	var contract *domain.ContractPlan
	if values.contractPath != "" {
		contract = new(domain.ContractPlan)
		if err := readContextCommandJSON(values.contractPath, contract); err != nil {
			return request, fmt.Errorf("read contract-plan JSON: %w", err)
		}
	}
	var coverage []contextretrieval.CoverageResult
	if values.coveragePath != "" {
		var report planning.RoutingCoverageReport
		if err := readContextCommandJSON(values.coveragePath, &report); err != nil {
			return request, fmt.Errorf("read routing coverage JSON: %w", err)
		}
		var err error
		coverage, err = contextadapter.AdaptRoutingCoverage(report, route, contract, admitted)
		if err != nil {
			return request, fmt.Errorf("bind routing coverage: %w", err)
		}
	}
	adapted, err := contextadapter.AdaptTaskRoute(route, admitted, contract, coverage, append(append([]contextretrieval.Facet{}, request.RequiredFacets...), request.OptionalFacets...))
	if err != nil {
		return request, fmt.Errorf("adapt route: %w", err)
	}
	// Caller-serialized route context never overrides the actual current route.
	request.Route = adapted
	return request, nil
}

func runContextPrepare(args []string, output io.Writer) error {
	values, err := parseContextCommandFlags("context-prepare", args, false)
	if err != nil {
		return err
	}
	request, err := loadContextCommandRequest(values)
	if err != nil {
		return err
	}
	pack, _, err := contextadapter.NewEngine().Prepare(context.Background(), request)
	if err != nil {
		return fmt.Errorf("prepare context: %w", err)
	}
	return writeContextCommandPack(output, pack, values.format)
}

func runContextExpand(args []string, output io.Writer) error {
	values, err := parseContextCommandFlags("context-expand", args, true)
	if err != nil {
		return err
	}
	request, err := loadContextCommandRequest(values)
	if err != nil {
		return err
	}
	var base contextretrieval.ContextPack
	if err := readContextCommandJSON(values.basePath, &base); err != nil {
		return fmt.Errorf("read base pack: %w", err)
	}
	if err := contextretrieval.VerifyDigest(base); err != nil {
		return fmt.Errorf("verify base pack: %w", err)
	}
	var expansion contextretrieval.ExpandRequest
	if err := readContextCommandJSON(values.expandPath, &expansion); err != nil {
		return fmt.Errorf("read expansion: %w", err)
	}
	delta, _, err := contextadapter.NewEngine().Expand(context.Background(), base, request, expansion)
	if err != nil {
		return fmt.Errorf("expand context: %w", err)
	}
	if values.format == "markdown" {
		return writeContextCommandDeltaMarkdown(output, delta)
	}
	return writeJSON(output, delta)
}

func runContextEvaluate(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("context-evaluate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("fixtures-dir", "", "explicit directory of retrieval-gold/v1 fixtures")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*directory) == "" {
		return fmt.Errorf("context-evaluate requires --fixtures-dir: %w", domain.ErrValidation)
	}
	report, err := evaluation.EvaluateDirectory(context.Background(), *directory)
	if err != nil {
		return fmt.Errorf("evaluate context: %w", err)
	}
	if err := writeJSON(output, report); err != nil {
		return err
	}
	if !report.GatesPassed {
		return fmt.Errorf("context evaluation quality gates failed: %w", domain.ErrValidation)
	}
	return nil
}

func writeContextCommandPack(output io.Writer, pack contextretrieval.ContextPack, format string) error {
	if format == "markdown" {
		markdown, err := contextretrieval.Markdown(pack)
		if err != nil {
			return err
		}
		_, err = io.WriteString(output, markdown)
		return err
	}
	canonical, err := contextretrieval.CanonicalJSON(pack)
	if err != nil {
		return err
	}
	_, err = output.Write(append(canonical, '\n'))
	return err
}

// A delta can be blocked while carrying the unchanged, complete base package.
// Its outcome and diagnostics must remain visible in every output format.
func writeContextCommandDeltaMarkdown(output io.Writer, delta contextretrieval.ContextDelta) error {
	pack, err := contextretrieval.Markdown(delta.Pack)
	if err != nil {
		return err
	}
	metadata := struct {
		SchemaVersion    string                                 `json:"schema_version"`
		Status           contextretrieval.Status                `json:"status"`
		BaseDigest       string                                 `json:"base_digest"`
		ContentDigest    string                                 `json:"content_digest"`
		AddedEvidenceIDs []string                               `json:"added_evidence_ids"`
		Diagnostics      []contextretrieval.RetrievalDiagnostic `json:"diagnostics"`
	}{delta.SchemaVersion, delta.Status, delta.BaseDigest, delta.ContentDigest, delta.AddedEvidenceIDs, delta.Diagnostics}
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	fence := "```"
	for strings.Contains(string(encoded), fence) {
		fence += "`"
	}
	var markdown strings.Builder
	fmt.Fprintf(&markdown, "# Context expansion\n\nStatus: **%s**\n\nBase digest: `%s`\n\nContent digest: `%s`\n\n%sjson\n%s\n%s\n\nThe package below is carried by this delta. A blocked expansion retains its base package.\n\n%s", delta.Status, delta.BaseDigest, delta.ContentDigest, fence, encoded, fence, pack)
	_, err = io.WriteString(output, markdown.String())
	return err
}

// Command JSON is explicit control input, not a source scan. Reject secret paths
// before opening and use descriptor-relative no-follow reads for every component.
func readContextCommandJSON(path string, value any) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(absolute), string(filepath.Separator)), string(filepath.Separator))
	for _, part := range parts {
		if contextCommandSecretComponent(part) {
			return fmt.Errorf("excluded command input path: %w", domain.ErrValidation)
		}
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		} else {
			flags |= unix.O_NONBLOCK
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return fmt.Errorf("unsafe or unavailable command input: %w", openErr)
		}
		fd = next
	}
	file := os.NewFile(uintptr(fd), absolute)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o444 == 0 || info.Size() > maxContextCommandJSONBytes {
		return fmt.Errorf("command JSON must be readable regular bounded input: %w", domain.ErrValidation)
	}
	var descriptor unix.Stat_t
	if err := unix.Fstat(fd, &descriptor); err != nil {
		return err
	}
	if descriptor.Nlink != 1 {
		return fmt.Errorf("hardlinked command input is excluded: %w", domain.ErrValidation)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxContextCommandJSONBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxContextCommandJSONBytes {
		return fmt.Errorf("command JSON exceeds limit: %w", domain.ErrValidation)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		return fmt.Errorf("command input requires a JSON object: %w", domain.ErrValidation)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("command input requires exactly one JSON object: %w", domain.ErrValidation)
	}
	return nil
}

func contextCommandSecretComponent(value string) bool {
	return contextretrieval.SecretPathComponent(value)
}
