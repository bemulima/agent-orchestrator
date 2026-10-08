package http

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/example/auth-service/config"
	adminv1 "github.com/example/auth-service/internal/transport/http/admin/v1"
	v1 "github.com/example/auth-service/internal/transport/http/api/v1"
	privatehttp "github.com/example/auth-service/internal/transport/http/private"
)

type Router struct {
	cfg       *config.Config
	apiRouter *v1.Router
}

func NewRouter(cfg *config.Config, apiRouter *v1.Router) *Router {
	return &Router{cfg: cfg, apiRouter: apiRouter}
}

func (r *Router) Setup(e *echo.Echo) {
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.Logger())

	internalGroup := e.Group("/internal")
	privatehttp.Register(internalGroup)

	apiGroup := e.Group(r.cfg.HTTPBasePath)
	r.apiRouter.Register(apiGroup)
	adminv1.Register(e.Group(r.cfg.HTTPBasePath + "/admin"))
}
