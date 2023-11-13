package main

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"

	"github.com/flosch/pongo2/v6"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
)

//go:embed static/*
var StaticFS embed.FS

type Server struct {
	echo          *echo.Echo
	dir           identity.Directory
	xrpcc         *xrpc.Client
	defaultHandle syntax.Handle
}

func NewServer(xrpcc *xrpc.Client, debug bool) (*Server, error) {
	e := echo.New()

	// httpd
	var (
		httpTimeout        = 1 * time.Minute
		httpMaxHeaderBytes = 1 * (1024 * 1024)
	)

	dh, err := syntax.ParseHandle("atproto.com")
	if err != nil {
		return nil, err
	}
	e.Server.WriteTimeout = httpTimeout
	e.Server.ReadTimeout = httpTimeout
	e.Server.MaxHeaderBytes = httpMaxHeaderBytes
	srv := &Server{
		echo:          e,
		xrpcc:         xrpcc,
		dir:           identity.DefaultDirectory(),
		defaultHandle: dh,
	}
	e.HidePort = true
	e.HideBanner = true
	e.Use(slogecho.New(slog))
	e.Use(middleware.Recover())
	e.Use(echoprometheus.NewMiddleware("athome"))
	e.Use(middleware.BodyLimit("64M"))
	e.HTTPErrorHandler = srv.errorHandler
	e.Renderer = NewRenderer("templates/", &TemplateFS, debug)
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "SAMEORIGIN",
		HSTSMaxAge:         31536000, // 365 days
		// TODO:
		// ContentSecurityPolicy
		// XSSProtection
	}))
	// redirect trailing slash to non-trailing slash.
	// all of our current endpoints have no trailing slash.
	e.Use(middleware.RemoveTrailingSlashWithConfig(middleware.TrailingSlashConfig{
		RedirectCode: http.StatusFound,
	}))

	fs := func() fs.FS {
		if debug {
			return os.DirFS("static")
		}
		return echo.MustSubFS(StaticFS, "static")
	}()
	// basic static routes
	e.StaticFS("/static", fs)
	e.FileFS("/robots.txt", "robots.txt", fs)
	e.FileFS("/favicon.ico", "static/favicon.ico", fs)

	// health routes
	e.GET("/_health", srv.HandleHealthCheck)
	e.GET("/metrics", echoprometheus.NewHandler())

	// actual content
	e.GET("/", srv.WebHome)
	e.GET("/bsky", srv.WebProfile)
	e.GET("/bsky/post/:rkey", srv.WebPost)
	e.GET("/bsky/repo.car", srv.WebRepoCar)
	e.GET("/bsky/rss.xml", srv.WebRepoRSS)

	return srv, nil
}

type GenericStatus struct {
	Daemon  string `json:"daemon"`
	Status  string `json:"status"`
	Message string `json:"msg,omitempty"`
}

func (srv *Server) errorHandler(err error, c echo.Context) {
	code := http.StatusInternalServerError
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
	}
	if code >= 500 {
		slog.Warn("athome-http-internal-error", "err", err)
	}
	data := pongo2.Context{
		"statusCode": code,
	}
	c.Render(code, "error.html", data)
}

func (srv *Server) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	srv.echo.ServeHTTP(rw, req)
}

func (srv *Server) ListenAndServe(addr string) error {
	slog.Info("starting server", "bind", addr)
	return srv.echo.Start(addr)
}

func (srv *Server) Shutdown() error {
	slog.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.echo.Shutdown(ctx)
}

func (s *Server) HandleHealthCheck(c echo.Context) error {
	return c.JSON(200, GenericStatus{Status: "ok", Daemon: "athome"})
}
