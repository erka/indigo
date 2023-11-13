package main

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/util"
	"github.com/bluesky-social/indigo/xrpc"

	"github.com/flosch/pongo2/v6"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
	"github.com/urfave/cli/v2"
)

//go:embed static/*
var StaticFS embed.FS

type Server struct {
	echo          *echo.Echo
	httpd         *http.Server
	dir           identity.Directory // TODO: unused?
	xrpcc         *xrpc.Client
	defaultHandle syntax.Handle
}

func serve(cctx *cli.Context) error {
	debug := cctx.Bool("debug")
	httpAddress := cctx.String("bind")
	appviewHost := cctx.String("appview-host")

	dh, err := syntax.ParseHandle("atproto.com")
	if err != nil {
		return err
	}
	xrpccUserAgent := "athome/" + version
	xrpcc := &xrpc.Client{
		Client:    util.RobustHTTPClient(),
		Host:      appviewHost,
		UserAgent: &xrpccUserAgent,
	}
	e := echo.New()

	// httpd
	var (
		httpTimeout        = 1 * time.Minute
		httpMaxHeaderBytes = 1 * (1024 * 1024)
	)

	srv := &Server{
		echo:          e,
		xrpcc:         xrpcc,
		dir:           identity.DefaultDirectory(),
		defaultHandle: dh,
	}
	srv.httpd = &http.Server{
		Handler:        srv,
		Addr:           httpAddress,
		WriteTimeout:   httpTimeout,
		ReadTimeout:    httpTimeout,
		MaxHeaderBytes: httpMaxHeaderBytes,
	}

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

	staticHandler := http.FileServer(func() http.FileSystem {
		if debug {
			return http.FS(os.DirFS("static"))
		}
		fsys, err := fs.Sub(StaticFS, "static")
		if err != nil {
			slog.Error("static template error", "err", err)
			os.Exit(-1)
		}
		return http.FS(fsys)
	}())

	e.GET("/static/*", echo.WrapHandler(http.StripPrefix("/static/", staticHandler)))
	e.GET("/_health", srv.HandleHealthCheck)
	e.GET("/metrics", echoprometheus.NewHandler())

	// basic static routes
	e.GET("/robots.txt", echo.WrapHandler(staticHandler))
	e.GET("/favicon.ico", echo.WrapHandler(staticHandler))

	// actual content
	e.GET("/", srv.WebHome)
	e.GET("/bsky", srv.WebProfile)
	e.GET("/bsky/post/:rkey", srv.WebPost)
	e.GET("/bsky/repo.car", srv.WebRepoCar)
	e.GET("/bsky/rss.xml", srv.WebRepoRSS)

	errCh := make(chan error)
	// Start the server
	slog.Info("starting server", "bind", httpAddress)
	go func() {
		if err := srv.httpd.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	// Wait for a signal to exit.
	slog.Info("registering OS exit signal handler")
	exitSignals := make(chan os.Signal, 1)
	signal.Notify(exitSignals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-exitSignals:
		slog.Info("received OS exit signal", "signal", sig)

		// Shut down the HTTP server
		if err := srv.Shutdown(); err != nil {
			slog.Error("HTTP server shutdown error", "err", err)
		}

		// Trigger the return that causes an exit.
		slog.Info("graceful shutdown complete")

	case err := <-errCh:
		slog.Error("HTTP server shutting down unexpectedly", "err", err)
	}
	return nil
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

func (srv *Server) Shutdown() error {
	slog.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.httpd.Shutdown(ctx)
}

func (s *Server) HandleHealthCheck(c echo.Context) error {
	return c.JSON(200, GenericStatus{Status: "ok", Daemon: "athome"})
}
