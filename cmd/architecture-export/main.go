// Command architecture-export writes a portable CURRENT graph to stdout.
// Build from a committed, unmodified CDO source revision to pin its producer.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	postgres "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	projection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	uc "github.com/bemulima/agent-orchestrator/internal/usecase/architecturecatalog"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(out, "architecture-export: read persisted CURRENT and exact owner Git blobs; write architecture-graph.v1 JSON to stdout. DATABASE_URL and REPOSITORY_ALLOWED_ROOTS/REPOSITORY_STORAGE_PATH configure read access. Use --fleet-inputs <lock.json> --fleet-roots <roots.json> for pinned owner CURRENT without DATABASE_URL; optional inventory flags apply to persisted mode.")
		return err
	}
	flags := flag.NewFlagSet("architecture-export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fleetPath := flags.String("fleet-inputs", "", "normalized pinned owner fleet lock")
	fleetRoots := flags.String("fleet-roots", "", "local source identity to object store JSON map")
	inventoryRoot := flags.String("inventory-root", "", "CDO object store containing pinned inventory")
	inventoryPath := flags.String("inventory-path", "", "CDO owner inventory path at producer commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*fleetPath == "") != (*fleetRoots == "") {
		return fmt.Errorf("use paired --fleet-inputs and --fleet-roots with optional paired inventory flags")
	}
	if flags.NArg() != 0 || (*inventoryRoot == "") != (*inventoryPath == "") {
		return fmt.Errorf("use paired --inventory-root and --inventory-path, with no positional arguments")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("producer build provenance unavailable")
	}
	producer, err := producerFromBuild(info)
	if err != nil {
		return err
	}
	if *fleetPath != "" {
		for _, file := range []string{*fleetPath, *fleetRoots} {
			for _, component := range strings.Split(file, "/") {
				if component == "test-results" || component == ".env" || strings.HasPrefix(component, ".env.") {
					return fmt.Errorf("forbidden fleet input path")
				}
			}
		}
		raw, e := os.ReadFile(*fleetPath)
		if e != nil {
			return fmt.Errorf("fleet lock unavailable")
		}
		rootBytes, e := os.ReadFile(*fleetRoots)
		if e != nil {
			return fmt.Errorf("fleet roots unavailable")
		}
		var roots map[string]string
		if e = json.Unmarshal(rootBytes, &roots); e != nil {
			return fmt.Errorf("invalid fleet roots")
		}
		allowed := strings.Split(os.Getenv("REPOSITORY_ALLOWED_ROOTS"), ",")
		if storage := os.Getenv("REPOSITORY_STORAGE_PATH"); storage != "" {
			allowed = append(allowed, storage)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		operation := uc.FleetExport{Inputs: raw, Roots: roots, Producer: producer, Resolver: gitadapter.ArchitectureBlobResolver{AllowedRoots: allowed}}
		if *inventoryRoot != "" {
			operation.Inventory = &uc.InventoryRequest{Root: *inventoryRoot, Path: *inventoryPath, SourceIdentity: "git:github.com/" + producer.RepositoryID, CommitSHA: producer.CommitSHA}
		}
		graph, e := operation.Handle(ctx)
		if e != nil {
			return e
		}
		data, e := projection.CanonicalGraphJSON(graph)
		if e != nil {
			return e
		}
		_, e = out.Write(append(data, '\n'))
		return e
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid export configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("CURRENT store unavailable")
	}
	defer pool.Close()
	current := uc.Current{Topology: postgres.TopologyRepoPG{Pool: pool}, Projects: postgres.ProjectRepoPG{Pool: pool}, Builder: projection.Builder{}}
	roots := append(append([]string{}, cfg.RepositoryAllowedRoots...), cfg.RepositoryStoragePath)
	operation := uc.Export{Current: current, Producer: producer, Resolver: gitadapter.ArchitectureBlobResolver{AllowedRoots: roots}}
	if *inventoryRoot != "" {
		operation.Inventory = &uc.InventoryRequest{Root: *inventoryRoot, Path: *inventoryPath, SourceIdentity: "git:github.com/" + producer.RepositoryID, CommitSHA: producer.CommitSHA}
	}
	graph, err := operation.Handle(ctx)
	if err != nil {
		return fmt.Errorf("capture CURRENT graph: %w", err)
	}
	data, err := projection.CanonicalGraphJSON(graph)
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	return err
}
func producerFromBuild(info *debug.BuildInfo) (domain.ArchitectureGraphProducer, error) {
	revision, modified, vcs := "", "", ""
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		case "vcs":
			vcs = setting.Value
		}
	}
	producer := domain.ArchitectureGraphProducer{RepositoryID: "bemulima/agent-orchestrator", CommitSHA: revision}
	// ValidGraphPin validates the SHA format without accepting a caller-supplied
	// producer label. The producer SHA is independent from artifact storage.
	if vcs != "git" || modified != "false" || !projection.ValidGraphPin(domain.ArchitectureGraphPin{SourceIdentity: "producer", CommitSHA: revision, Path: "producer", BlobOID: revision, ContentSHA256: strings.Repeat("0", 64)}) {
		return domain.ArchitectureGraphProducer{}, fmt.Errorf("producer must be built with Git provenance from an unmodified committed CDO revision")
	}
	return producer, nil
}
