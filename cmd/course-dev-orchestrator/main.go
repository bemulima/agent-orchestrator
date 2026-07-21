package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	gitlabadapter "github.com/bemulima/agent-orchestrator/internal/adapters/gitlab"
	httpadapter "github.com/bemulima/agent-orchestrator/internal/adapters/http"
	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	temporaladapter "github.com/bemulima/agent-orchestrator/internal/adapters/temporal"
	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	onboardinggenerator "github.com/bemulima/agent-orchestrator/internal/onboarding"
	topologybuilder "github.com/bemulima/agent-orchestrator/internal/topology"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
	onboardinguc "github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
	projectuc "github.com/bemulima/agent-orchestrator/internal/usecase/project"
	topologyuc "github.com/bemulima/agent-orchestrator/internal/usecase/topology"
	orchestratorworkflow "github.com/bemulima/agent-orchestrator/internal/workflow"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("course-dev-orchestrator: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if command == "help" || command == "--help" || command == "-h" {
		printUsage()
		return nil
	}
	if command == "version" {
		fmt.Printf("course-dev-orchestrator %s (%s)\n", version, commit)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}
	if command == "config-check" {
		return writeJSON(os.Stdout, cfg.SafeSummary())
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	switch command {
	case "serve":
		return runServer(cfg, logger)
	case "worker":
		return runWorker(cfg, logger)
	case "workflow-probe":
		return runWorkflowProbe(cfg, logger)
	case "project-connect", "project-list", "project-show", "project-scan", "project-report",
		"project-onboard", "project-diff", "project-approve", "project-reject", "project-apply":
		return runProjectCommand(cfg, command, args[1:], os.Stdout)
	case "topology", "contracts", "contract-drift", "dependencies", "consumers":
		return runTopologyCommand(cfg, command, args[1:], os.Stdout)
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func runServer(cfg config.Config, logger *zap.Logger) error {
	ctx := context.Background()
	pool, err := pgadapter.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	readiness := healthuc.CheckReadiness{Dependencies: []repository.HealthChecker{
		pgadapter.HealthRepoPG{Pool: pool},
		temporaladapter.HealthChecker{HostPort: cfg.TemporalHostPort},
	}}
	healthHandler := handlers.HealthHandler{
		Readiness: readiness,
		Timeout:   httpadapter.DefaultHealthTimeout,
	}
	projectOperations := newProjectOperations(cfg, pool)
	projectHandler := handlers.ProjectHandler{
		Connect:      projectOperations.Connect,
		Get:          projectOperations.Get,
		List:         projectOperations.List,
		Scan:         projectOperations.Scan,
		LatestReport: projectOperations.Latest,
	}
	onboardingOperations := newOnboardingOperations(cfg, pool)
	onboardingHandler := handlers.OnboardingHandler{
		Prepare: onboardingOperations.Prepare,
		Get:     onboardingOperations.Get,
		Approve: onboardingOperations.Approve,
		Reject:  onboardingOperations.Reject,
		Apply:   onboardingOperations.Apply,
	}
	topologyOperations := newTopologyOperations(pool)
	topologyHandler := handlers.TopologyHandler{
		Rebuild: topologyOperations.Rebuild, Get: topologyOperations.Get,
		Services: topologyOperations.Services, Contracts: topologyOperations.Contracts,
		Drift: topologyOperations.Drift, ProjectQuery: topologyOperations.Project,
	}
	router := httpadapter.NewRouter(httpadapter.RouterDependencies{
		HealthHandler:     healthHandler,
		ProjectHandler:    &projectHandler,
		OnboardingHandler: &onboardingHandler,
		TopologyHandler:   &topologyHandler,
		Logger:            logger,
	})
	server := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting http server", zap.String("port", cfg.HTTPPort))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("http server: %w", err)
	case signalReceived := <-stop:
		logger.Info("shutting down http server", zap.String("signal", signalReceived.String()))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}

type topologyOperations struct {
	Rebuild   topologyuc.Rebuild
	Get       topologyuc.Get
	Services  topologyuc.Services
	Contracts topologyuc.Contracts
	Drift     topologyuc.ContractDrift
	Project   topologyuc.ProjectQuery
}

func newTopologyOperations(pool *pgxpool.Pool) topologyOperations {
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	catalog := pgadapter.TopologyRepoPG{Pool: pool}
	return topologyOperations{
		Rebuild:   topologyuc.Rebuild{Projects: projects, Catalog: catalog, Builder: topologybuilder.Builder{}},
		Get:       topologyuc.Get{Catalog: catalog},
		Services:  topologyuc.Services{Catalog: catalog},
		Contracts: topologyuc.Contracts{Catalog: catalog},
		Drift:     topologyuc.ContractDrift{Catalog: catalog},
		Project:   topologyuc.ProjectQuery{Projects: projects, Catalog: catalog},
	}
}

type onboardingOperations struct {
	Prepare onboardinguc.Prepare
	Get     onboardinguc.Get
	Approve onboardinguc.Approve
	Reject  onboardinguc.Reject
	Apply   onboardinguc.Apply
}

func newOnboardingOperations(cfg config.Config, pool *pgxpool.Pool) onboardingOperations {
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	runs := pgadapter.OnboardingRepoPG{Pool: pool}
	sources := gitadapter.ProjectSource{
		AllowedRoots: cfg.RepositoryAllowedRoots,
		StoragePath:  cfg.RepositoryStoragePath,
	}
	generator := onboardinggenerator.NewGenerator(onboardinggenerator.GeneratorConfig{
		MaxFileBytes: cfg.OnboardingMaxFileBytes, MaxTotalBytes: cfg.OnboardingMaxTotalBytes,
	})
	worktrees := gitadapter.OnboardingWorktree{
		StoragePath: cfg.WorktreeStoragePath, AuthorName: cfg.OnboardingAuthorName, AuthorEmail: cfg.OnboardingAuthorEmail,
	}
	publisher := gitlabadapter.OnboardingPublisher{
		BaseURL: cfg.GitLabBaseURL, Token: cfg.GitLabToken, DryRun: cfg.GitLabDryRun,
	}
	return onboardingOperations{
		Prepare: onboardinguc.Prepare{Projects: projects, Sources: sources, Runs: runs, Generator: generator},
		Get:     onboardinguc.Get{Runs: runs},
		Approve: onboardinguc.Approve{Runs: runs},
		Reject:  onboardinguc.Reject{Runs: runs},
		Apply:   onboardinguc.Apply{Projects: projects, Runs: runs, Worktree: worktrees, Publisher: publisher},
	}
}

type projectOperations struct {
	Connect projectuc.ConnectProject
	Get     projectuc.GetProject
	List    projectuc.ListProjects
	Scan    projectuc.ScanProject
	Latest  projectuc.GetLatestDiscoveryReport
}

func newProjectOperations(cfg config.Config, pool *pgxpool.Pool) projectOperations {
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	sources := gitadapter.ProjectSource{
		AllowedRoots: cfg.RepositoryAllowedRoots,
		StoragePath:  cfg.RepositoryStoragePath,
	}
	scanner := discovery.NewScanner(discovery.Config{
		MaxFiles:      cfg.DiscoveryMaxFiles,
		MaxFileBytes:  cfg.DiscoveryMaxFileBytes,
		MaxTotalBytes: cfg.DiscoveryMaxTotalBytes,
		MaxDepth:      cfg.DiscoveryMaxDepth,
	})
	scan := projectuc.ScanProject{Projects: projects, Sources: sources, Scanner: scanner}
	return projectOperations{
		Connect: projectuc.ConnectProject{Projects: projects, Sources: sources, Scan: scan},
		Get:     projectuc.GetProject{Projects: projects},
		List:    projectuc.ListProjects{Projects: projects},
		Scan:    scan,
		Latest:  projectuc.GetLatestDiscoveryReport{Projects: projects},
	}
}

func runProjectCommand(cfg config.Config, command string, args []string, output io.Writer) error {
	ctx := context.Background()
	pool, err := pgadapter.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	operations := newProjectOperations(cfg, pool)
	onboardingOperations := newOnboardingOperations(cfg, pool)

	switch command {
	case "project-connect":
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		localPath := flags.String("path", "", "absolute local Git repository path")
		gitURL := flags.String("git-url", "", "Git repository URL")
		role := flags.String("role", string(domain.RepositoryRoleService), "repository role")
		if err := flags.Parse(args); err != nil {
			return fmt.Errorf("parse project-connect flags: %w", err)
		}
		result, err := operations.Connect.Handle(ctx, projectuc.ConnectInput{
			LocalPath:      *localPath,
			GitURL:         *gitURL,
			RepositoryRole: domain.RepositoryRole(*role),
		})
		if err != nil {
			return err
		}
		return writeJSON(output, result)
	case "project-list":
		if len(args) != 0 {
			return fmt.Errorf("project-list accepts no arguments: %w", domain.ErrValidation)
		}
		projects, err := operations.List.Handle(ctx)
		if err != nil {
			return err
		}
		return writeJSON(output, map[string]any{"projects": projects})
	case "project-show", "project-scan", "project-report", "project-onboard":
		identifier, err := projectIdentifier(command, args)
		if err != nil {
			return err
		}
		project, err := operations.Get.Handle(ctx, identifier)
		if err != nil {
			return err
		}
		switch command {
		case "project-show":
			return writeJSON(output, project)
		case "project-scan":
			result, scanErr := operations.Scan.Handle(ctx, project.ID)
			if scanErr != nil {
				return scanErr
			}
			return writeJSON(output, result)
		case "project-report":
			result, reportErr := operations.Latest.Handle(ctx, project.ID)
			if reportErr != nil {
				return reportErr
			}
			return writeJSON(output, result)
		default:
			dryRun, parseErr := booleanFlag(command, args, "dry-run")
			if parseErr != nil {
				return parseErr
			}
			run, prepareErr := onboardingOperations.Prepare.Handle(ctx, onboardinguc.PrepareInput{ProjectID: project.ID, DryRun: dryRun})
			if prepareErr != nil {
				return prepareErr
			}
			return writeJSON(output, run)
		}
	case "project-diff", "project-approve", "project-reject", "project-apply":
		runID, actor, comment, dryRun, parseErr := onboardingFlags(command, args)
		if parseErr != nil {
			return parseErr
		}
		switch command {
		case "project-diff":
			run, getErr := onboardingOperations.Get.Handle(ctx, runID)
			if getErr != nil {
				return getErr
			}
			_, writeErr := io.WriteString(output, run.UnifiedDiff)
			return writeErr
		case "project-approve":
			run, approveErr := onboardingOperations.Approve.Handle(ctx, onboardinguc.DecideInput{RunID: runID, Actor: actor, Comment: comment})
			if approveErr != nil {
				return approveErr
			}
			return writeJSON(output, run)
		case "project-reject":
			run, rejectErr := onboardingOperations.Reject.Handle(ctx, onboardinguc.DecideInput{RunID: runID, Actor: actor, Comment: comment})
			if rejectErr != nil {
				return rejectErr
			}
			return writeJSON(output, run)
		default:
			result, applyErr := onboardingOperations.Apply.Handle(ctx, onboardinguc.ApplyInput{RunID: runID, DryRun: dryRun})
			if applyErr != nil {
				return applyErr
			}
			return writeJSON(output, result)
		}
	default:
		return fmt.Errorf("unknown project command %q", command)
	}
}

func runTopologyCommand(cfg config.Config, command string, args []string, output io.Writer) error {
	ctx := context.Background()
	pool, err := pgadapter.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	operations := newTopologyOperations(pool)

	switch command {
	case "topology":
		if len(args) != 0 {
			return fmt.Errorf("topology accepts no arguments: %w", domain.ErrValidation)
		}
		catalog, rebuildErr := operations.Rebuild.Handle(ctx)
		if rebuildErr != nil {
			return rebuildErr
		}
		return writeJSON(output, catalog)
	case "contracts":
		if len(args) != 0 {
			return fmt.Errorf("contracts accepts no arguments: %w", domain.ErrValidation)
		}
		contracts, queryErr := operations.Contracts.Handle(ctx)
		if queryErr != nil {
			return queryErr
		}
		return writeJSON(output, map[string]any{"contracts": contracts})
	case "contract-drift":
		if len(args) != 0 {
			return fmt.Errorf("contract-drift accepts no arguments: %w", domain.ErrValidation)
		}
		drifts, queryErr := operations.Drift.Handle(ctx)
		if queryErr != nil {
			return queryErr
		}
		return writeJSON(output, map[string]any{"contract_drift": drifts})
	case "dependencies", "consumers":
		identifier, parseErr := projectIdentifier(command, args)
		if parseErr != nil {
			return parseErr
		}
		view, queryErr := operations.Project.Handle(ctx, identifier)
		if queryErr != nil {
			return queryErr
		}
		if command == "dependencies" {
			return writeJSON(output, map[string]any{"project": view.Project, "dependencies": view.Dependencies, "impact": view.Impact})
		}
		return writeJSON(output, map[string]any{"project": view.Project, "consumers": view.Consumers, "impact": view.Impact})
	default:
		return fmt.Errorf("unknown topology command %q", command)
	}
}

func projectIdentifier(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	service := flags.String("service", "", "project ID or unique name")
	_ = flags.Bool("dry-run", false, "prepare without approval")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse %s flags: %w", command, err)
	}
	identifier := *service
	if identifier == "" && flags.NArg() == 1 {
		identifier = flags.Arg(0)
	}
	if identifier == "" || flags.NArg() > 1 {
		return "", fmt.Errorf("--service is required: %w", domain.ErrValidation)
	}
	return identifier, nil
}

func booleanFlag(command string, args []string, name string) (bool, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	_ = flags.String("service", "", "project ID or unique name")
	value := flags.Bool(name, false, name)
	if err := flags.Parse(args); err != nil {
		return false, fmt.Errorf("parse %s flags: %w", command, err)
	}
	return *value, nil
}

func onboardingFlags(command string, args []string) (runID, actor, comment string, dryRun bool, err error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runIDValue := flags.String("run-id", "", "onboarding run ID")
	actorValue := flags.String("actor", "owner", "approval actor")
	commentValue := flags.String("comment", "", "approval comment")
	dryRunValue := flags.Bool("dry-run", false, "validate without writes")
	if parseErr := flags.Parse(args); parseErr != nil {
		return "", "", "", false, fmt.Errorf("parse %s flags: %w", command, parseErr)
	}
	if strings.TrimSpace(*runIDValue) == "" || flags.NArg() != 0 {
		return "", "", "", false, fmt.Errorf("--run-id is required: %w", domain.ErrValidation)
	}
	return strings.TrimSpace(*runIDValue), strings.TrimSpace(*actorValue), strings.TrimSpace(*commentValue), *dryRunValue, nil
}

func runWorker(cfg config.Config, logger *zap.Logger) error {
	client, err := temporalclient.Dial(temporalclient.Options{
		HostPort:  cfg.TemporalHostPort,
		Namespace: cfg.TemporalNamespace,
		Logger:    temporaladapter.NewLogger(logger),
	})
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer client.Close()

	temporalWorker := worker.New(client, cfg.TemporalTaskQueue, worker.Options{})
	temporalWorker.RegisterWorkflow(orchestratorworkflow.SystemProbeWorkflow)
	temporalWorker.RegisterActivity(&activities.SystemActivities{})
	logger.Info("starting temporal worker",
		zap.String("namespace", cfg.TemporalNamespace),
		zap.String("task_queue", cfg.TemporalTaskQueue),
	)
	if err := temporalWorker.Run(worker.InterruptCh()); err != nil {
		return fmt.Errorf("run temporal worker: %w", err)
	}
	return nil
}

func runWorkflowProbe(cfg config.Config, logger *zap.Logger) error {
	client, err := temporalclient.Dial(temporalclient.Options{
		HostPort:  cfg.TemporalHostPort,
		Namespace: cfg.TemporalNamespace,
		Logger:    temporaladapter.NewLogger(logger),
	})
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer client.Close()

	requestID := uuid.NewString()
	workflowID := "system-probe-" + requestID
	run, err := client.ExecuteWorkflow(context.Background(), temporalclient.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: cfg.TemporalTaskQueue,
	}, orchestratorworkflow.SystemProbeWorkflow, activities.SystemProbeInput{RequestID: requestID})
	if err != nil {
		return fmt.Errorf("start system probe workflow: %w", err)
	}
	logger.Info("started system probe workflow",
		zap.String("workflow_id", run.GetID()),
		zap.String("run_id", run.GetRunID()),
	)

	var output activities.SystemProbeOutput
	if err := run.Get(context.Background(), &output); err != nil {
		return fmt.Errorf("wait for system probe workflow: %w", err)
	}
	return writeJSON(os.Stdout, output)
}

func writeJSON(writer interface{ Write([]byte) (int, error) }, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage() {
	fmt.Println(`Usage: course-dev-orchestrator <command>

Commands:
  serve           Start the internal HTTP API (default)
  worker          Start the Temporal worker
  workflow-probe  Run a Temporal workflow/activity smoke test
  config-check    Validate configuration and print a secret-free summary
  project-connect Connect a local path or Git URL and run read-only discovery
  project-list    List connected projects
  project-show    Show a project by ID or unique name
  project-scan    Run read-only discovery for a project
  project-report  Show the latest discovery report
  project-onboard Prepare an evidence-backed onboarding proposal
  project-diff    Print an onboarding proposal diff
  project-approve Approve an onboarding proposal
  project-reject  Reject an onboarding proposal
  project-apply   Validate or apply an approved proposal in a worktree
  topology        Rebuild and print the materialized service topology
  contracts       List discovered service contracts
  contract-drift  List producer/consumer contract drift
  dependencies    Show direct dependencies and impact for a service
  consumers       Show direct and transitive consumers for a service
  version         Print build version
  help            Show this help`)
}
