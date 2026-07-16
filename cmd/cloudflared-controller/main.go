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
	if err := ctrl.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("controller exited", "error", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
