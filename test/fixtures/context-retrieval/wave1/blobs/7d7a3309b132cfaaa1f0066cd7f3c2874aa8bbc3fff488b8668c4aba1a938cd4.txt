package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	nats "github.com/nats-io/nats.go"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/example/auth-service/config"
	natsadapter "github.com/example/auth-service/internal/infrastructure/messaging/nats"
	repo "github.com/example/auth-service/internal/infrastructure/persistence/postgres"
	"github.com/example/auth-service/internal/usecase"
)

const markerPath = "/run/v1/ownership.json"

var runIDPattern = regexp.MustCompile(`^[a-z0-9]{8,20}$`)

type ownershipMarker struct {
	SchemaVersion int    `json:"schema_version"`
	Purpose       string `json:"purpose"`
	RunID         string `json:"run_id"`
	Project       string `json:"compose_project"`
	Network       string `json:"network"`
}

type safeOutput struct {
	Status     string `json:"status"`
	RunID      string `json:"run_id"`
	IdentityID string `json:"identity_id,omitempty"`
	Role       string `json:"role,omitempty"`
	Stage      string `json:"stage,omitempty"`
}

func main() {
	if err := execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}

func execute(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Local hash generation consumes only stdin and has no database or token capability.
	// The coordinator must capture stdout in a private file, never a log.
	if len(args) == 1 && args[0] == "--hash-password" {
		password, err := readPassword(stdin)
		if err != nil || len(password) < 8 || len(password) > 72 {
			return writeSafeFailure(stderr, safeOutput{Status: "failed", Stage: "input"})
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return writeSafeFailure(stderr, safeOutput{Status: "failed", Stage: "password_hash"})
		}
		if _, err = fmt.Fprintln(stdout, string(hash)); err != nil {
			return errors.New("private output unavailable")
		}
		return nil
	}

	if len(args) == 0 || args[0] != "provision-verification-identity" {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", Stage: "command"})
	}
	flags := flag.NewFlagSet("provision-verification-identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	runID := flags.String("run-id", "", "disposable run ID")
	userID := flags.String("user-id", "", "run-scoped Auth user UUID")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", Stage: "arguments"})
	}
	if !runIDPattern.MatchString(*runID) {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", Stage: "input"})
	}
	if err := validateSafety(*runID); err != nil {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", RunID: *runID, Stage: "safety_gate"})
	}
	password, err := readPassword(stdin)
	if err != nil {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", RunID: *runID, Stage: "input"})
	}

	cfg, err := config.Load()
	if err != nil {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", RunID: *runID, Stage: "configuration"})
	}
	db, err := gorm.Open(postgres.Open(buildDSN(cfg)), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", RunID: *runID, Stage: "auth_database"})
	}
	defer func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	}()

	nc, err := nats.Connect(cfg.NATSURL, nats.Timeout(3*time.Second))
	if err != nil {
		return writeSafeFailure(stderr, safeOutput{Status: "failed", RunID: *runID, Stage: "rbac_connection"})
	}
	defer nc.Close()
	roles := natsadapter.NewRBACClient(nc, cfg.NATSAssignRoleSubject, cfg.NATSCheckRoleSubject)
	fixture := usecase.NewVerificationFixture(repo.NewVerificationIdentityRepository(db), roles)
	result, err := fixture.Provision(context.Background(), usecase.VerificationFixtureInput{
		RunID: *runID, UserID: *userID, Password: password,
	})
	if err != nil {
		var fixtureErr *usecase.VerificationFixtureError
		if errors.As(err, &fixtureErr) {
			return writeSafeFailure(stderr, safeOutput{
				Status: fixtureErr.Status, RunID: fixtureErr.RunID,
				IdentityID: fixtureErr.IdentityID, Stage: fixtureErr.Stage,
			})
		}
		return writeSafeFailure(stderr, safeOutput{Status: "failed", RunID: *runID, Stage: "owner_fixture"})
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return err
	}
	return nil
}

func validateSafety(runID string) error {
	return validateSafetyAt(runID, markerPath)
}

func validateSafetyAt(runID, path string) error {
	if os.Getenv("AUTH_VERIFICATION_FIXTURE_ENABLED") != "true" ||
		os.Getenv("AUTH_APP_ENV") != "v1-r0" ||
		os.Getenv("AUTH_DB_HOST") != "auth-db" ||
		os.Getenv("AUTH_DB_PORT") != "5432" ||
		os.Getenv("AUTH_DB_USER") != "v1_auth" ||
		os.Getenv("AUTH_DB_PASSWORD") == "" ||
		os.Getenv("AUTH_DB_NAME") != "v1_auth" ||
		os.Getenv("AUTH_DB_MIGRATE_ON_START") != "false" ||
		os.Getenv("NATS_URL") != "nats://nats:4222" ||
		os.Getenv("NATS_SUBJECT_ASSIGN_ROLE") != "rbac.assign-role" ||
		os.Getenv("NATS_SUBJECT_CHECK_ROLE") != "rbac.checkRole" ||
		os.Getenv("AUTH_JWT_SECRET") != "" ||
		os.Getenv("AUTH_JWT_PRIVATE_KEY") != "" ||
		os.Getenv("AUTH_JWT_PUBLIC_KEY") != "" {
		return errors.New("fixture is not enabled for the disposable runtime")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("disposable ownership marker is unavailable")
	}
	var marker ownershipMarker
	if err := json.Unmarshal(data, &marker); err != nil ||
		marker.SchemaVersion != 1 || marker.Purpose != "v1-a-auth-fixture" ||
		marker.RunID != runID || marker.Project != "lp-v1-"+runID ||
		marker.Network != marker.Project+"-net" {
		return errors.New("disposable ownership marker does not match this run")
	}
	return nil
}

func readPassword(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 513))
	if err != nil || len(data) == 0 || len(data) > 512 {
		return "", errors.New("invalid fixture password input")
	}
	password := strings.TrimSuffix(string(data), "\n")
	password = strings.TrimSuffix(password, "\r")
	if strings.ContainsAny(password, "\x00\r\n") {
		return "", errors.New("invalid fixture password input")
	}
	return password, nil
}

func buildDSN(cfg *config.Config) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s", cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode)
}

func writeSafeFailure(writer io.Writer, result safeOutput) error {
	_ = json.NewEncoder(writer).Encode(result)
	return errors.New("verification fixture did not complete")
}
