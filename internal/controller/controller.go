// Package controller reconciles docker container labels into Cloudflare
// tunnel ingress rules and DNS records.
package controller

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	"github.com/ronaldmiranda/cloudflared-controller/internal/cloudflare"
	"github.com/ronaldmiranda/cloudflared-controller/internal/config"
	"github.com/ronaldmiranda/cloudflared-controller/internal/docker"
)

const catchAllService = "http_status:404"

type Controller struct {
	cfg    *config.Config
	cf     *cloudflare.Client
	docker *docker.Discovery
	log    *slog.Logger

	// dnsZones accumulates every zone a labeled hostname has resolved to, so
	// stale-record sweeps keep covering a zone after its last container stops.
	dnsZones map[string]cloudflare.Zone
}

func New(cfg *config.Config, cf *cloudflare.Client, d *docker.Discovery, log *slog.Logger) *Controller {
	return &Controller{cfg: cfg, cf: cf, docker: d, log: log, dnsZones: map[string]cloudflare.Zone{}}
}

// Run blocks, reconciling on docker events and on a periodic resync ticker,
// until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	events := c.docker.Watch(ctx, c.cfg.DebounceInterval)
	ticker := time.NewTicker(c.cfg.ResyncInterval)
	defer ticker.Stop()

	c.log.Info("starting reconcile loop",
		"tunnel", c.cfg.TunnelID,
		"label_prefix", c.cfg.LabelPrefix,
		"resync_interval", c.cfg.ResyncInterval,
		"manage_dns", c.cfg.ManageDNS)

	if err := c.reconcile(ctx); err != nil {
		// The first reconcile validates credentials and connectivity; failing
		// fast here surfaces misconfiguration instead of silently retrying.
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-events:
			if !ok {
				return ctx.Err()
			}
			if err := c.reconcile(ctx); err != nil {
				c.log.Error("reconcile failed", "error", err)
			}
		case <-ticker.C:
			if err := c.reconcile(ctx); err != nil {
				c.log.Error("resync failed", "error", err)
			}
		}
	}
}

func (c *Controller) reconcile(ctx context.Context) error {
	start := time.Now()

	routes, err := c.docker.DesiredRoutes(ctx)
	if err != nil {
		return err
	}

	desired := &cloudflare.TunnelConfig{Ingress: make([]cloudflare.IngressRule, 0, len(routes)+1)}
	for _, r := range routes {
		rule := cloudflare.IngressRule{
			Hostname: r.Hostname,
			Path:     r.Path,
			Service:  r.Service,
		}
		if r.NoTLSVerify {
			rule.OriginRequest = &cloudflare.OriginRequest{NoTLSVerify: true}
		}
		desired.Ingress = append(desired.Ingress, rule)
	}
	desired.Ingress = append(desired.Ingress, cloudflare.IngressRule{Service: catchAllService})

	current, err := c.cf.GetTunnelConfig(ctx)
	if err != nil {
		return err
	}

	if ingressEqual(current.Ingress, desired.Ingress) {
		c.log.Debug("tunnel config up to date", "routes", len(routes))
	} else {
		if err := c.cf.PutTunnelConfig(ctx, desired); err != nil {
			return err
		}
		for _, r := range routes {
			c.log.Info("route active", "hostname", r.Hostname, "service", r.Service, "container", r.ContainerName)
		}
		c.log.Info("tunnel config updated", "routes", len(routes))
	}

	if c.cfg.ManageDNS {
		c.reconcileDNS(ctx, routes)
	}

	c.log.Debug("reconcile done", "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// ingressEqual compares only the fields this controller manages, so unrelated
// defaults the API adds to the stored config don't cause a rewrite every loop.
func ingressEqual(a, b []cloudflare.IngressRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Hostname != b[i].Hostname ||
			a[i].Path != b[i].Path ||
			a[i].Service != b[i].Service ||
			!reflect.DeepEqual(normalizeOrigin(a[i].OriginRequest), normalizeOrigin(b[i].OriginRequest)) {
			return false
		}
	}
	return true
}

func normalizeOrigin(o *cloudflare.OriginRequest) *cloudflare.OriginRequest {
	if o == nil || !o.NoTLSVerify {
		return nil
	}
	return o
}
