// Package main wires the user service's configuration, persistence, and transports.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/nats-io/nats.go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/example/user-service/config"
	"github.com/example/user-service/internal/infrastructure/avatarstorage"
	"github.com/example/user-service/internal/infrastructure/filestorage"
	"github.com/example/user-service/internal/infrastructure/imageprocessor"
	natsadapter "github.com/example/user-service/internal/infrastructure/messaging/nats"
	"github.com/example/user-service/internal/infrastructure/oauthavatar"
	repo "github.com/example/user-service/internal/infrastructure/persistence/postgres"
	rbacclient "github.com/example/user-service/internal/infrastructure/rbac"
	httpadapter "github.com/example/user-service/internal/transport/http"
	adminv1 "github.com/example/user-service/internal/transport/http/admin/v1/handlers"
	apiv1 "github.com/example/user-service/internal/transport/http/api/v1/handlers"
	mw "github.com/example/user-service/internal/transport/http/middleware"
	service "github.com/example/user-service/internal/usecase"
	pkglog "github.com/example/user-service/pkg/log"
)

// App holds the running service components and their open resources.
type App struct {
	cfg      *config.Config
	logger   pkglog.Logger
	db       *gorm.DB
	echo     *echo.Echo
	natsConn *nats.Conn
}

// New assembles the user service and its configured adapters.
func New(_ context.Context) (*App, error) {
	cfg := config.MustLoad()
	logger := pkglog.New(cfg.AppEnv)

	db, err := gorm.Open(postgres.Open(buildDSN(cfg)), &gorm.Config{
		Logger: loggerForGorm(cfg),
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		return nil, err
	}

	filestorageClient := filestorage.NewHTTPClient(cfg.FileStorageURL, cfg.FileStorageInternalToken, 5*time.Second)
	rbacHTTP := rbacclient.NewHTTPClient(cfg.RBACURL, 3*time.Second)
	rbacClient := rbacclient.NewCachingClient(rbacHTTP, time.Minute)

	var natsConn *nats.Conn
	if cfg.NATSURL != "" {
		if conn, err := nats.Connect(cfg.NATSURL); err == nil {
			natsConn = conn
		} else {
			return nil, fmt.Errorf("connect nats: %w", err)
		}
	}

	userRepo := repo.NewUserRepository(db)
	activeUserRepo := repo.NewActiveUserRepository(db)
	profileRepo := repo.NewUserProfileRepository(db)
	_ = repo.NewUserProviderRepository(db)
	identityRepo := repo.NewUserIdentityRepository(db)
	userService := service.NewUserService(userRepo, profileRepo, identityRepo)
	manageService := service.NewUserManageService(userRepo, profileRepo, rbacClient)
	activeUserService := service.NewActiveUserService(activeUserRepo)

	var imageProcClient imageprocessor.Client
	if cfg.ImageProcessorURL != "" {
		imageProcClient = imageprocessor.NewHTTPClient(cfg.ImageProcessorURL, 10*time.Second, cfg.ImageProcessorInternalToken)
	}

	apiHandler := apiv1.NewHandler(userService, filestorageClient, imageProcClient, cfg.AvatarPresetGroup, cfg.AvatarFileKind)
	adminHandler := adminv1.NewHandler(manageService, filestorageClient)

	authMW := mw.NewAuthMiddleware(cfg, logger, rbacClient, userRepo, natsConn)
	rbacMW := mw.NewRBACMiddleware(rbacClient)

	e := echo.New()
	router := httpadapter.NewRouter(cfg, apiHandler, adminHandler, activeUserService, authMW, rbacMW)
	router.Setup(e)

	if natsConn != nil {
		rpc := natsadapter.Server{Conn: natsConn}
		avatarClient := oauthavatar.NewHTTPClient(5*time.Second, oauthavatar.DefaultMaxSize)
		avatarStorage := avatarstorage.NewClient(filestorageClient)
		oauthProfileImporter := service.NewOAuthProfileImporter(
			profileRepo, avatarStorage, avatarClient, cfg.AvatarFileKind,
		)
		createHandler := natsadapter.NewCreateUserHandler(userRepo, profileRepo, oauthProfileImporter)
		_ = rpc.Subscribe(cfg.NATSUserCreate, "ms-go-user", createHandler.Handle)
	}

	return &App{cfg: cfg, logger: logger, db: db, echo: e, natsConn: natsConn}, nil
}

// Run serves HTTP requests until the context is canceled or the server stops.
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.echo.Shutdown(shutdownCtx)
	}()
	go func() {
		errCh <- a.echo.Start(net.JoinHostPort(a.cfg.AppHost, a.cfg.AppPort))
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Close releases the service's NATS and database connections.
func (a *App) Close() {
	if a.natsConn != nil {
		_ = a.natsConn.Drain()
	}
	if a.db != nil {
		if sqlDB, err := a.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

func buildDSN(cfg *config.Config) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s", cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode)
}

func loggerForGorm(cfg *config.Config) logger.Interface {
	level := logger.Silent
	switch cfg.GormLogLevel {
	case "error":
		level = logger.Error
	case "warn":
		level = logger.Warn
	case "info":
		level = logger.Info
	}
	return logger.Default.LogMode(level)
}
