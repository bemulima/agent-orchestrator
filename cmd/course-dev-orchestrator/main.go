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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	codexadapter "github.com/bemulima/agent-orchestrator/internal/adapters/codex"
	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	gitlabadapter "github.com/bemulima/agent-orchestrator/internal/adapters/gitlab"
	httpadapter "github.com/bemulima/agent-orchestrator/internal/adapters/http"
	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	temporaladapter "github.com/bemulima/agent-orchestrator/internal/adapters/temporal"
	"github.com/bemulima/agent-orchestrator/internal/agent"
	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	executionengine "github.com/bemulima/agent-orchestrator/internal/execution"
	onboardinggenerator "github.com/bemulima/agent-orchestrator/internal/onboarding"
	planningengine "github.com/bemulima/agent-orchestrator/internal/planning"
	topologybuilder "github.com/bemulima/agent-orchestrator/internal/topology"
	executionuc "github.com/bemulima/agent-orchestrator/internal/usecase/execution"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
	onboardinguc "github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
	planninguc "github.com/bemulima/agent-orchestrator/internal/usecase/planning"
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
	case "plan", "plan-show", "plan-approve", "plan-reject", "plan-run",
		"run-status", "run-pause", "run-resume", "run-cancel", "task-show", "task-log", "task-retry", "task-cancel":
		return runPlanningCommand(cfg, command, args[1:], os.Stdout)
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
	temporalClient, err := temporalclient.Dial(temporalclient.Options{
		HostPort: cfg.TemporalHostPort, Namespace: cfg.TemporalNamespace, Logger: temporaladapter.NewLogger(logger),
	})
	if err != nil {
		return fmt.Errorf("connect temporal plan client: %w", err)
	}
	defer temporalClient.Close()
	planningOperations := newPlanningOperations(cfg, pool, temporaladapter.PlanRunner{
		Client: temporalClient, TaskQueue: cfg.TemporalTaskQueue,
	})
	planningHandler := handlers.PlanningHandler{
		CreateCommand: planningOperations.CreateCommand, GetCommand: planningOperations.GetCommand,
		CreatePlan: planningOperations.CreatePlan, GetPlan: planningOperations.GetPlan,
		ApprovePlan: planningOperations.ApprovePlan, RejectPlan: planningOperations.RejectPlan,
		StartPlan: planningOperations.StartPlan, GetRun: planningOperations.GetRun,
		ControlRun: planningOperations.ControlRun, GetTask: planningOperations.GetTask,
		CancelTask: planningOperations.CancelTask, GetAttempts: planningOperations.GetAttempts,
		GetArtifacts: planningOperations.GetArtifacts, RetryTask: planningOperations.RetryTask,
	}
	router := httpadapter.NewRouter(httpadapter.RouterDependencies{
		HealthHandler:     healthHandler,
		ProjectHandler:    &projectHandler,
		OnboardingHandler: &onboardingHandler,
		TopologyHandler:   &topologyHandler,
		PlanningHandler:   &planningHandler,
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

type planningOperations struct {
	CreateCommand planninguc.CreateCommand
	GetCommand    planninguc.GetCommand
	CreatePlan    planninguc.CreatePlan
	GetPlan       planninguc.GetPlan
	ApprovePlan   planninguc.ApprovePlan
	RejectPlan    planninguc.RejectPlan
	StartPlan     planninguc.StartPlan
	GetRun        planninguc.GetRun
	ControlRun    planninguc.ControlRun
	GetTask       planninguc.GetTask
	CancelTask    planninguc.CancelTask
	GetAttempts   executionuc.GetAttempts
	GetArtifacts  executionuc.GetArtifacts
	TaskLog       executionuc.TaskLog
	RetryTask     executionuc.RetryTask
}

func newPlanningOperations(cfg config.Config, pool *pgxpool.Pool, runner repository.PlanRunner) planningOperations {
	plans := pgadapter.PlanningRepoPG{Pool: pool}
	taskExecutions := pgadapter.TaskExecutionRepoPG{Pool: pool}
	catalog := pgadapter.TopologyRepoPG{Pool: pool}
	return planningOperations{
		CreateCommand: planninguc.CreateCommand{Plans: plans},
		GetCommand:    planninguc.GetCommand{Plans: plans},
		CreatePlan: planninguc.CreatePlan{
			Plans: plans, Topology: catalog,
			Planner: planningengine.Planner{MaxParallelTasks: cfg.MaxParallelTasks},
			Validator: planningengine.Validator{
				MaxParallelTasks: cfg.MaxParallelTasks, MaxRequiredTaskDepth: cfg.MaxRequiredTaskDepth,
			},
		},
		GetPlan:     planninguc.GetPlan{Plans: plans},
		ApprovePlan: planninguc.ApprovePlan{Plans: plans},
		RejectPlan:  planninguc.RejectPlan{Plans: plans},
		StartPlan: planninguc.StartPlan{
			Plans: plans, Runner: runner, MaxParallelTasks: cfg.MaxParallelTasks,
			MaxActivityAttempts: cfg.MaxTaskAttempts,
		},
		GetRun:       planninguc.GetRun{Plans: plans},
		ControlRun:   planninguc.ControlRun{Plans: plans, Runner: runner},
		GetTask:      planninguc.GetTask{Plans: plans},
		CancelTask:   planninguc.CancelTask{Plans: plans, Runner: runner},
		GetAttempts:  executionuc.GetAttempts{Tasks: taskExecutions},
		GetArtifacts: executionuc.GetArtifacts{Tasks: taskExecutions},
		TaskLog:      executionuc.TaskLog{Tasks: taskExecutions},
		RetryTask: executionuc.RetryTask{
			Plans: plans, Tasks: taskExecutions, Runner: runner, MaxAttempts: cfg.MaxTaskAttempts,
		},
	}
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

func runPlanningCommand(cfg config.Config, command string, args []string, output io.Writer) error {
	ctx := context.Background()
	pool, err := pgadapter.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	var runner repository.PlanRunner
	if planningCommandNeedsTemporal(command) {
		temporalClient, dialErr := temporalclient.Dial(temporalclient.Options{
			HostPort: cfg.TemporalHostPort, Namespace: cfg.TemporalNamespace,
		})
		if dialErr != nil {
			return fmt.Errorf("connect temporal: %w", dialErr)
		}
		defer temporalClient.Close()
		runner = temporaladapter.PlanRunner{Client: temporalClient, TaskQueue: cfg.TemporalTaskQueue}
	}
	operations := newPlanningOperations(cfg, pool, runner)

	switch command {
	case "plan":
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		file := flags.String("file", "", "natural-language command file")
		projectIDs := flags.String("project-ids", "", "optional comma-separated project IDs")
		if err := flags.Parse(args); err != nil {
			return fmt.Errorf("parse plan flags: %w", err)
		}
		if strings.TrimSpace(*file) == "" || flags.NArg() != 0 {
			return fmt.Errorf("--file is required: %w", domain.ErrValidation)
		}
		text, err := readCommandFile(*file)
		if err != nil {
			return err
		}
		created, err := operations.CreateCommand.Handle(ctx, planninguc.CreateCommandInput{
			Source: domain.CommandSourceCLI, Text: text,
		})
		if err != nil {
			return err
		}
		bundle, err := operations.CreatePlan.Handle(ctx, created.ID, domain.PlanRequest{
			RequestedProjectIDs: commaSeparated(*projectIDs),
		})
		if err != nil {
			return err
		}
		return writeJSON(output, bundle)
	case "plan-show", "plan-approve", "plan-reject", "plan-run":
		planID, actor, comment, err := planCommandFlags(command, args)
		if err != nil {
			return err
		}
		switch command {
		case "plan-show":
			bundle, getErr := operations.GetPlan.Handle(ctx, planID)
			if getErr != nil {
				return getErr
			}
			return writeJSON(output, bundle)
		case "plan-approve":
			bundle, approveErr := operations.ApprovePlan.Handle(ctx, planninguc.DecidePlanInput{
				PlanID: planID, Actor: actor, Comment: comment,
			})
			if approveErr != nil {
				return approveErr
			}
			return writeJSON(output, bundle)
		case "plan-reject":
			bundle, rejectErr := operations.RejectPlan.Handle(ctx, planninguc.DecidePlanInput{
				PlanID: planID, Actor: actor, Comment: comment,
			})
			if rejectErr != nil {
				return rejectErr
			}
			return writeJSON(output, bundle)
		default:
			run, runErr := operations.StartPlan.Handle(ctx, planID)
			if runErr != nil {
				return runErr
			}
			return writeJSON(output, run)
		}
	case "run-status", "run-pause", "run-resume", "run-cancel":
		runID, err := requiredIDFlag(command, args, "run-id")
		if err != nil {
			return err
		}
		if command == "run-status" {
			run, getErr := operations.GetRun.Handle(ctx, runID)
			if getErr != nil {
				return getErr
			}
			return writeJSON(output, run)
		}
		action := domain.RunControlPause
		if command == "run-resume" {
			action = domain.RunControlResume
		} else if command == "run-cancel" {
			action = domain.RunControlCancel
		}
		run, controlErr := operations.ControlRun.Handle(ctx, planninguc.ControlRunInput{RunID: runID, Action: action})
		if controlErr != nil {
			return controlErr
		}
		return writeJSON(output, run)
	case "task-show", "task-log", "task-retry", "task-cancel":
		taskID, err := requiredIDFlag(command, args, "task-id")
		if err != nil {
			return err
		}
		if command == "task-show" {
			task, getErr := operations.GetTask.Handle(ctx, taskID)
			if getErr != nil {
				return getErr
			}
			return writeJSON(output, task)
		}
		if command == "task-log" {
			logResult, logErr := operations.TaskLog.Handle(ctx, taskID)
			if logErr != nil {
				return logErr
			}
			return writeJSON(output, logResult)
		}
		if command == "task-retry" {
			task, retryErr := operations.RetryTask.Handle(ctx, taskID)
			if retryErr != nil {
				return retryErr
			}
			return writeJSON(output, task)
		}
		task, cancelErr := operations.CancelTask.Handle(ctx, taskID)
		if cancelErr != nil {
			return cancelErr
		}
		return writeJSON(output, task)
	default:
		return fmt.Errorf("unknown planning command %q", command)
	}
}

func planningCommandNeedsTemporal(command string) bool {
	switch command {
	case "plan-run", "run-pause", "run-resume", "run-cancel", "task-retry", "task-cancel":
		return true
	default:
		return false
	}
}

func readCommandFile(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve command file: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect command file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("command file must be a regular file no larger than 1 MiB: %w", domain.ErrValidation)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", fmt.Errorf("open command file: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", fmt.Errorf("read command file: %w", err)
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return "", fmt.Errorf("command file is empty: %w", domain.ErrValidation)
	}
	return text, nil
}

func commaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func planCommandFlags(command string, args []string) (planID, actor, comment string, err error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	planIDValue := flags.String("plan-id", "", "plan ID")
	actorValue := flags.String("actor", "owner", "decision actor")
	commentValue := flags.String("comment", "", "decision comment")
	if parseErr := flags.Parse(args); parseErr != nil {
		return "", "", "", fmt.Errorf("parse %s flags: %w", command, parseErr)
	}
	if strings.TrimSpace(*planIDValue) == "" || flags.NArg() != 0 {
		return "", "", "", fmt.Errorf("--plan-id is required: %w", domain.ErrValidation)
	}
	return strings.TrimSpace(*planIDValue), strings.TrimSpace(*actorValue), strings.TrimSpace(*commentValue), nil
}

func requiredIDFlag(command string, args []string, name string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	value := flags.String(name, "", name)
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse %s flags: %w", command, err)
	}
	if strings.TrimSpace(*value) == "" || flags.NArg() != 0 {
		return "", fmt.Errorf("--%s is required: %w", name, domain.ErrValidation)
	}
	return strings.TrimSpace(*value), nil
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
	pool, err := pgadapter.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect worker postgres: %w", err)
	}
	defer pool.Close()
	client, err := temporalclient.Dial(temporalclient.Options{
		HostPort:  cfg.TemporalHostPort,
		Namespace: cfg.TemporalNamespace,
		Logger:    temporaladapter.NewLogger(logger),
	})
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer client.Close()
	runner, err := codexadapter.NewProcessRunner(cfg.CodexRunnerCommand)
	if err != nil {
		return err
	}
	resultValidator, err := agent.NewValidator()
	if err != nil {
		return fmt.Errorf("create agent result validator: %w", err)
	}
	taskExecutions := pgadapter.TaskExecutionRepoPG{Pool: pool}
	worktrees := gitadapter.TaskWorktree{
		StoragePath: cfg.WorktreeStoragePath, AuthorName: cfg.OnboardingAuthorName, AuthorEmail: cfg.OnboardingAuthorEmail,
	}
	executor := &executionengine.Service{
		Repository: taskExecutions, Worktrees: worktrees, Runner: runner, Validator: resultValidator,
		Verifier: executionengine.Verifier{Worktrees: worktrees},
		Models: map[string]string{
			config.ModelProfileFast: cfg.CodexModelFast, config.ModelProfileStandard: cfg.CodexModelStandard,
			config.ModelProfileDeep: cfg.CodexModelDeep,
		},
		ReviewModel: cfg.CodexModelReview, MaxTaskAttempts: cfg.MaxTaskAttempts,
		MaxReviewAttempts: cfg.MaxReviewAttempts, MaxReplans: cfg.MaxReplans,
		MaxRequiredTaskDepth: cfg.MaxRequiredTaskDepth,
	}

	temporalWorker := worker.New(client, cfg.TemporalTaskQueue, worker.Options{})
	temporalWorker.RegisterWorkflow(orchestratorworkflow.SystemProbeWorkflow)
	temporalWorker.RegisterWorkflow(orchestratorworkflow.PlanWorkflow)
	temporalWorker.RegisterActivity(&activities.SystemActivities{})
	temporalWorker.RegisterActivity(&activities.PlanActivities{
		Plans: pgadapter.PlanningRepoPG{Pool: pool}, TaskExecutions: taskExecutions, Executor: executor,
	})
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
  plan            Create an approval-gated DAG from a command file
  plan-show       Show a persisted plan, tasks, dependencies, approval, and run
  plan-approve    Approve a plan for execution
  plan-reject     Reject and cancel a plan
  plan-run        Start or reuse the Temporal plan workflow
  run-status      Show a plan run
  run-pause       Pause new task dispatch
  run-resume      Resume task dispatch
  run-cancel      Cancel a plan run
  task-show       Show a planned task
  task-log        Show task attempts, verification results, and artifacts
  task-retry      Retry a blocked or changes-requested task
  task-cancel     Signal cancellation for a dispatched task
  version         Print build version
  help            Show this help`)
}
