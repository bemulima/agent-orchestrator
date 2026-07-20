package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"

	"github.com/example/course-dev-orchestrator/internal/activities"
	httpadapter "github.com/example/course-dev-orchestrator/internal/adapters/http"
	"github.com/example/course-dev-orchestrator/internal/adapters/http/handlers"
	pgadapter "github.com/example/course-dev-orchestrator/internal/adapters/postgres"
	temporaladapter "github.com/example/course-dev-orchestrator/internal/adapters/temporal"
	"github.com/example/course-dev-orchestrator/internal/config"
	"github.com/example/course-dev-orchestrator/internal/domain/repository"
	healthuc "github.com/example/course-dev-orchestrator/internal/usecase/health"
	orchestratorworkflow "github.com/example/course-dev-orchestrator/internal/workflow"
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
	router := httpadapter.NewRouter(httpadapter.RouterDependencies{
		HealthHandler: healthHandler,
		Logger:        logger,
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
  version         Print build version
  help            Show this help`)
}
