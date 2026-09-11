package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ronaldmiranda/cloudflared-controller/internal/cloudflare"
	"github.com/ronaldmiranda/cloudflared-controller/internal/config"
	"github.com/ronaldmiranda/cloudflared-controller/internal/controller"
	"github.com/ronaldmiranda/cloudflared-controller/internal/docker"
	"github.com/ronaldmiranda/cloudflared-controller/internal/status"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "error", err)
		os.Exit(1)
	}

	discovery, err := docker.NewDiscovery(cfg.LabelPrefix, log)
	if err != nil {
		log.Error("docker connection error", "error", err)
		os.Exit(1)
	}
	defer discovery.Close()

	cf := cloudflare.NewClient(cfg.APIToken, cfg.AccountID, cfg.TunnelID)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ctrl := controller.New(cfg, cf, discovery, log)
	statusSrv := status.New(cfg.StatusAddr, ctrl.Routes, log)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- ctrl.Run(runCtx); cancel() }()
	go func() { errCh <- statusSrv.Run(runCtx); cancel() }()

	var runErr error
	for range 2 {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) && runErr == nil {
			runErr = err
		}
	}
	if runErr != nil {
		log.Error("controller exited", "error", runErr)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
