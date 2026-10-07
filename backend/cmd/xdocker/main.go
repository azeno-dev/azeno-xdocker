// Command xdocker is the xDocker server: Docker, stack, port and registry
// management with retention policies, for Synology DSM.
//
// Configuration comes from environment variables:
//
//	XDOCKER_LISTEN       address to listen on            (default :8790)
//	XDOCKER_DATA_DIR     database and secret key         (default /data)
//	XDOCKER_DOCKER_HOST  Docker daemon                   (default unix:///var/run/docker.sock, or DOCKER_HOST)
//	XDOCKER_DOCKER_BIN   docker CLI used for compose     (default docker)
//	XDOCKER_STACKS_DIR   stacks folder, same path as on the host (default /volume1/docker)
//	XDOCKER_HOST_PROC    host /proc for the port check   (default: none)
//	XDOCKER_HOST_ADDR    address of the NAS as seen by xDocker (default localhost)
//	TZ                   default time zone for schedules
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/azeno/xdocker/internal/api"
	"github.com/azeno/xdocker/internal/app"
	"github.com/azeno/xdocker/internal/web"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := app.Config{
		Listen:     env("XDOCKER_LISTEN", ":8790"),
		DataDir:    env("XDOCKER_DATA_DIR", "/data"),
		DockerHost: env("XDOCKER_DOCKER_HOST", env("DOCKER_HOST", "unix:///var/run/docker.sock")),
		DockerBin:  env("XDOCKER_DOCKER_BIN", "docker"),
		StacksDir:  env("XDOCKER_STACKS_DIR", "/volume1/docker"),
		HostProc:   os.Getenv("XDOCKER_HOST_PROC"),
		HostAddr:   env("XDOCKER_HOST_ADDR", "localhost"),
		Version:    version,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	a.Bootstrap(ctx)

	sched := app.NewScheduler(a)
	go sched.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(a, sched, web.FS()),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("xDocker listening", "addr", cfg.Listen, "version", version, "docker", cfg.DockerHost, "stacks", cfg.StacksDir)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}
	return nil
}
