package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	adapters "github.com/bemulima/agent-orchestrator/internal/adapters/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
)

// EvaluateDirectory loads sorted committed JSON cases and runs the real offline
// engine against private synthetic directories. Errors are never counted as passes.
func EvaluateDirectory(ctx context.Context, directory string) (Report, error) {
	report := Report{SchemaVersion: ReportSchema, Cases: []CaseResult{}, Failures: []string{}}
	dir, err := openFixturePath(directory, true)
	if err != nil {
		return report, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return report, err
	}
	paths := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return report, fmt.Errorf("no retrieval-gold/v1 JSON fixtures in %s", directory)
	}
	if len(paths) > 256 {
		return report, fmt.Errorf("gold suite exceeds 256-case limit")
	}
	scenarios, ids := map[string]bool{}, map[string]bool{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		f, err := readFixture(path)
		if err != nil {
			return report, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if ids[f.ID] {
			return report, fmt.Errorf("duplicate fixture ID %s", f.ID)
		}
		ids[f.ID] = true
		scenarios[f.Scenario] = true
		o, err := execute(ctx, directory, f)
		if err != nil {
			return report, fmt.Errorf("fixture %s setup: %w", f.ID, err)
		}
		r := Evaluate(f, o)
		report.Cases = append(report.Cases, r)
		if f.Adversarial {
			report.AdversarialCount++
		}
		for _, failure := range r.Failures {
			report.Failures = append(report.Failures, f.ID+": "+failure)
		}
	}
	report.FixtureCount = len(report.Cases)
	report.ScenarioCount = len(scenarios)
	report.Metrics = aggregate(report.Cases)
	report.GatesPassed = len(report.Failures) == 0
	return report, nil
}

func readFixture(path string) (Fixture, error) {
	var f Fixture
	h, err := openFixturePath(path, false)
	if err != nil {
		return f, err
	}
	defer h.Close()
	info, err := h.Stat()
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return f, fmt.Errorf("fixture must be a regular file <= 2 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(h, (2<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&f); err != nil {
		return f, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return f, fmt.Errorf("trailing fixture data")
	}
	return f, ValidateFixture(f)
}

// The fixture runner is callable from the CLI: even an explicitly supplied
// fixture directory is not permission to read secret paths or follow symlinks.
func openFixturePath(path string, directory bool) (*os.File, error) {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part != "" && part != "." && part != ".." && !core.SafeRelativePath(part) {
			return nil, fmt.Errorf("fixture path excluded by policy")
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("unsafe or unreadable fixture path: %w", err)
		}
		fd = next
	}
	h := os.NewFile(uintptr(fd), absolute)
	info, err := h.Stat()
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	if !directory {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 {
			_ = h.Close()
			return nil, fmt.Errorf("fixture requires single-link regular file")
		}
	}
	return h, nil
}

func execute(ctx context.Context, directory string, f Fixture) (Observation, error) {
	o := Observation{Counters: map[string]int{}}
	root, err := os.MkdirTemp("", "cdo-retrieval-gold-")
	if err != nil {
		return o, err
	}
	defer os.RemoveAll(root)
	request := f.Request
	if request.RequestID == "" {
		request.RequestID = f.ID
	}
	if request.Task == "" {
		request.Task = f.Scenario
	}
	if request.Purpose == "" {
		request.Purpose = "gold evaluation"
	}
	if len(request.Sources) == 0 {
		request.Sources = []core.SourceAdmission{{Identity: "fixture", ReadPaths: []string{"."}}}
	}
	roots := map[string]string{}
	for i := range request.Sources {
		identity := request.Sources[i].Identity
		if identity == "" {
			return o, fmt.Errorf("empty source identity")
		}
		if _, exists := roots[identity]; exists {
			return o, fmt.Errorf("duplicate source")
		}
		sourceRoot := filepath.Join(root, fmt.Sprintf("source-%03d", i))
		if err := os.Mkdir(sourceRoot, 0700); err != nil {
			return o, err
		}
		roots[identity] = sourceRoot
		request.Sources[i].Root = sourceRoot
		if len(request.Sources[i].ReadPaths) == 0 {
			request.Sources[i].ReadPaths = []string{"."}
		}
	}
	if err := writeFiles(root, roots, f.Files); err != nil {
		return o, err
	}
	started := time.Now()
	switch f.Mode {
	case "routing_coverage":
		return executeR1(directory, request, f, roots, started)
	case "quality":
		plan := core.RetrievalPlan{SchemaVersion: "retrieval-plan.v1", RequestID: request.RequestID, Task: request.Task, Purpose: request.Purpose, Route: request.Route, Sources: request.Sources, RequiredFacets: request.RequiredFacets, OptionalFacets: request.OptionalFacets, Budget: request.Budget, Limits: request.Limits, TrustedPolicies: request.TrustedPolicies}
		if plan.Budget.MaxSourceBytes == 0 {
			plan.Budget.MaxSourceBytes = 1 << 20
		}
		if plan.Budget.MaxContextTokens == 0 {
			plan.Budget.MaxContextTokens = 1 << 18
		}
		sources := []core.EvidenceSource{}
		for _, s := range request.Sources {
			sources = append(sources, core.EvidenceSource{Identity: s.Identity, Revision: "revision:gold", Snapshot: "snapshot:gold", Dirty: s.Dirty, AdmissionDigest: "gold-admission"})
		}
		candidates := append([]core.EvidenceCandidate{}, f.Candidates...)
		for i := range candidates {
			c := &candidates[i]
			if c.EvidenceID == "" {
				c.EvidenceID = fmt.Sprintf("gold-evidence-%d", i)
			}
			if c.SourceIdentity == "" {
				c.SourceIdentity = request.Sources[0].Identity
			}
			if c.SourceRevision == "" {
				c.SourceRevision = "revision:gold"
			}
			if c.SourceSnapshot == "" {
				c.SourceSnapshot = "snapshot:gold"
			}
			if c.ContentHash == "" {
				sum := sha256.Sum256([]byte(c.Content))
				c.ContentHash = hex.EncodeToString(sum[:])
			}
			if c.Span.StartLine == 0 {
				c.Span.StartLine = 1
				c.Span.EndLine = 1 + strings.Count(strings.TrimSuffix(c.Content, "\n"), "\n")
				c.Span.EndByte = len(c.Content)
			}
			if c.Provenance == "" {
				c.Provenance = core.ProjectSource
			}
			if c.Freshness == "" {
				c.Freshness = core.Current
			}
			if c.ClaimType == "" {
				c.ClaimType = core.ImplementationBehavior
			}
			if c.EvidenceKind == "" {
				c.EvidenceKind = "source"
			}
			if c.Resolver == "" {
				c.Resolver = "gold-pinned-candidate"
			}
			if c.ResolverVersion == "" {
				c.ResolverVersion = "1.0.0"
			}
			if c.Query == "" {
				c.Query = core.QueryExact
			}
		}
		quality := core.AnalyzeQuality(plan, sources, candidates)
		pack, err := core.BuildPack(plan, sources, quality, nil, nil, core.BudgetUsed{})
		o = observe(pack, err)
		o.Counters["candidates"] = len(candidates)
		o.Latency = time.Since(started)
		return o, nil
	default:
		engine := adapters.NewEngine()
		pack, trace, err := engine.Prepare(ctx, request)
		o = observe(pack, err)
		o.Counters["candidates"] = trace.CandidateCount
		o.Counters["selected"] = trace.SelectedCount
		if f.Mode == "expand" && err == nil {
			if err := writeFiles(root, roots, f.Mutations); err != nil {
				return o, err
			}
			times := f.ExpandTimes
			if times == 0 {
				times = 1
			}
			for i := 0; i < times; i++ {
				delta, nextTrace, err := engine.Expand(ctx, pack, request, *f.Expand)
				o = observe(delta.Pack, err)
				if delta.Status != "" {
					o.Status = delta.Status
				}
				o.Diagnostics = append(o.Diagnostics, delta.Diagnostics...)
				o.Counters["expand_count"] = nextTrace.ExpandCount
				o.Counters["candidates"] = nextTrace.CandidateCount
				if err != nil || delta.Status == core.Stale || delta.Status == core.Invalid || delta.Status == core.Blocked {
					break
				}
				pack = delta.Pack
			}
		}
	}
	o.Latency = time.Since(started)
	return o, nil
}

func observe(pack core.ContextPack, err error) Observation {
	o := Observation{Status: pack.Status, Evidence: pack.Evidence, Coverage: pack.Coverage, Diagnostics: pack.UnresolvedQuestions, Conflicts: pack.AuthorityConflicts, Omissions: pack.Omissions, WriteOwners: pack.OwnerRepositories, Tokens: pack.BudgetUsed.ContextTokens, Counters: map[string]int{}}
	if err != nil {
		o.Error = err.Error()
		if errors.Is(err, core.ErrStaleBase) {
			o.Status = core.Stale
		} else if o.Status == "" {
			o.Status = core.Invalid
		}
	}
	return o
}

func writeFiles(tempRoot string, roots map[string]string, specs []FileSpec) error {
	setupBytes, setupFiles := int64(0), 0
	for _, spec := range specs {
		source := spec.Source
		if source == "" && len(roots) == 1 {
			for id := range roots {
				source = id
			}
		}
		root, exists := roots[source]
		if !exists {
			return fmt.Errorf("fixture file source %q not admitted", source)
		}
		count := spec.Count
		if count == 0 {
			count = 1
		}
		if count < 0 || count > 6000 || spec.Repeat < 0 || spec.Repeat > 300000 {
			return fmt.Errorf("fixture setup exceeds limit")
		}
		repeats := max(1, spec.Repeat)
		setupBytes += int64(len(spec.Content)) * int64(repeats) * int64(count)
		setupFiles += count
		if setupBytes > 32<<20 || setupFiles > 10000 {
			return fmt.Errorf("fixture setup exceeds 32 MiB / 10000-file limit")
		}
		for i := 0; i < count; i++ {
			path := spec.Path
			if count > 1 {
				if !strings.Contains(path, "%") {
					return fmt.Errorf("repeated path needs index format")
				}
				path = fmt.Sprintf(path, i)
			}
			if filepath.IsAbs(path) || strings.Contains(path, "\\") || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
				return fmt.Errorf("unsafe fixture setup path")
			}
			full := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
				return err
			}
			switch spec.Kind {
			case "symlink":
				outside := filepath.Join(tempRoot, "unadmitted-source.txt")
				if err := os.WriteFile(outside, []byte("synthetic forbidden neighbor"), 0600); err != nil {
					return err
				}
				if err := os.Symlink(outside, full); err != nil {
					return err
				}
			case "fifo":
				if err := syscall.Mkfifo(full, 0600); err != nil {
					return err
				}
			case "deleted":
				if err := os.Remove(full); err != nil {
					return err
				}
			default:
				content := spec.Content
				if spec.Repeat > 0 {
					content = strings.Repeat(content, spec.Repeat)
				}
				if err := os.WriteFile(full, []byte(content), 0600); err != nil {
					return err
				}
				if spec.Kind == "unreadable" {
					if err := os.Chmod(full, 0); err != nil {
						return err
					}
				} else if spec.Kind != "" {
					return fmt.Errorf("unknown fixture file kind %s", spec.Kind)
				}
			}
		}
	}
	return nil
}

func executeR1(directory string, request core.RetrievalRequest, f Fixture, roots map[string]string, started time.Time) (Observation, error) {
	o := Observation{Counters: map[string]int{}}
	// Fixtures reside inside CDO; the catalog remains the existing policy catalog.
	cdoRoot, err := filepath.Abs(filepath.Join(directory, "..", "..", "..", ".."))
	if err != nil {
		return o, err
	}
	catalog, err := agentcontrol.LoadCatalog(os.DirFS(cdoRoot))
	if err != nil {
		return o, err
	}
	projects := []domain.Project{}
	baseline := domain.PlannerOutput{}
	for _, s := range request.Sources {
		root := roots[s.Identity]
		projects = append(projects, domain.Project{ID: s.Identity, SourceIdentity: s.Identity, LocalPath: &root})
		baseline.Tasks = append(baseline.Tasks, domain.PlannedTask{ProjectID: s.Identity})
	}
	routing, _, report, err := planning.BuildRoutingMetadataWithCoverage(request.Task, baseline, projects, catalog)
	if err != nil {
		o.Error = err.Error()
		o.Status = core.Invalid
		return o, nil
	}
	o.Status = core.Status(report.Status)
	o.Counters["selected"] = report.EvidenceProjection.Selected
	o.Counters["acquired"] = report.EvidenceProjection.Acquired
	o.Counters["required_omitted"] = len(report.EvidenceProjection.RequiredOmittedIDs)
	for _, project := range report.Projects {
		o.Counters["indexed"] += project.IndexedFiles
		o.Counters["visited"] += project.VisitedFiles
		o.Counters["skipped_large"] += project.SkippedLargeFiles
		o.Counters["unreadable"] += project.ReadErrors
		o.Counters["facts_omitted"] += len(project.Facts.OmittedPaths)
		for _, ex := range project.Extractions {
			o.Counters["symbols_omitted"] += ex.OmittedMatches
		}
		for _, target := range project.Targets {
			o.Counters["targets_omitted"] += len(target.OmittedPaths)
		}
		for _, omission := range project.Omissions {
			o.Diagnostics = append(o.Diagnostics, core.RetrievalDiagnostic{Code: omission.Reason, Status: core.Partial, SourceIdentity: project.SourceIdentity, RelativePath: omission.Path})
		}
		o.Coverage = append(o.Coverage, core.CoverageResult{SourceIdentity: project.SourceIdentity, Stage: "routing_inventory", Status: core.Status(project.Status), Complete: project.Status == "COMPLETE", Visited: project.VisitedFiles, Indexed: project.IndexedFiles, Omitted: len(project.Omissions)})
	}
	for _, req := range report.Requirements {
		if req.Status != "FOUND" {
			o.Diagnostics = append(o.Diagnostics, core.RetrievalDiagnostic{Code: req.Status, SourceIdentity: req.ProjectID, RelativePath: req.Path, Message: req.Reason})
		}
	}
	if report.EvidenceProjection.Status == "PARTIAL" {
		o.Diagnostics = append(o.Diagnostics, core.RetrievalDiagnostic{Code: "global_evidence_limit", Status: core.Partial})
	}
	_ = routing // Routing behavior itself remains covered by pre-R1 byte fixtures.
	o.Latency = time.Since(started)
	return o, nil
}
