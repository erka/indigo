package main

import (
	"fmt"
	slogging "log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bluesky-social/indigo/util"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/carlmjohnson/versioninfo"
	"github.com/urfave/cli/v2"

	_ "github.com/joho/godotenv/autoload"
)

var (
	slog = slogging.New(slogging.NewJSONHandler(os.Stdout, nil))
)

func main() {
	if err := run(os.Args); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(-1)
	}
}

func run(args []string) error {

	app := cli.App{
		Name:    "athome",
		Usage:   "public web interface to bluesky account content",
		Version: versioninfo.Short(),
	}

	cli.VersionFlag = &cli.BoolFlag{
		Name:    "version",
		Aliases: []string{"v"},
		Usage:   "print only the version",
	}

	app.Commands = []*cli.Command{
		{
			Name:    "serve",
			Aliases: []string{"s"},
			Usage:   "run the server",
			Action:  serve,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "appview-host",
					Usage:   "method, hostname, and port of AppView instance",
					Value:   "https://api.bsky.app",
					EnvVars: []string{"ATP_APPVIEW_HOST"},
				},
				&cli.StringFlag{
					Name:     "bind",
					Usage:    "Specify the local IP/port to bind to",
					Required: false,
					Value:    ":8200",
					EnvVars:  []string{"ATHOME_BIND"},
				},
				&cli.BoolFlag{
					Name:     "debug",
					Usage:    "Enable debug mode",
					Value:    false,
					Required: false,
					EnvVars:  []string{"DEBUG"},
				},
			},
		},
		{
			Name:    "version",
			Aliases: []string{"v"},
			Usage:   "print version",
			Action: func(cctx *cli.Context) error {
				fmt.Println("athome version", cctx.App.Version)
				return nil
			},
		},
	}

	return app.Run(args)
}

func serve(cctx *cli.Context) error {
	debug := cctx.Bool("debug")
	httpAddress := cctx.String("bind")
	appviewHost := cctx.String("appview-host")

	xrpccUserAgent := "athome/" + cctx.App.Version
	xrpcc := &xrpc.Client{
		Client:    util.RobustHTTPClient(),
		Host:      appviewHost,
		UserAgent: &xrpccUserAgent,
	}
	srv, err := NewServer(xrpcc, debug)
	if err != nil {
		return err
	}

	errCh := make(chan error)
	// Start the server
	go func() {
		if err := srv.ListenAndServe(httpAddress); err != nil {
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
