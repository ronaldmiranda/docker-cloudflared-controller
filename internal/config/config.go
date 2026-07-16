package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime configuration, loaded from environment variables.
type Config struct {
	// Cloudflare API token with permissions:
	// Account > Cloudflare Tunnel > Edit, Zone > DNS > Edit
	APIToken  string
	AccountID string
	TunnelID  string

	// LabelPrefix is the docker label namespace, e.g. "cloudflared"
	// -> cloudflared.hostname, cloudflared.port, ...
	LabelPrefix string

	// ResyncInterval is how often a full reconcile runs even without
	// docker events.
	ResyncInterval time.Duration

	// DebounceInterval batches bursts of docker events into one reconcile.
	DebounceInterval time.Duration

	// ManageDNS controls whether the controller creates/deletes CNAME
	// records pointing to the tunnel.
	ManageDNS bool
}

func Load() (*Config, error) {
	cfg := &Config{
		APIToken:         os.Getenv("CLOUDFLARE_API_TOKEN"),
		AccountID:        os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		TunnelID:         os.Getenv("TUNNEL_ID"),
		LabelPrefix:      envOr("LABEL_PREFIX", "cloudflared"),
		ResyncInterval:   5 * time.Minute,
		DebounceInterval: 2 * time.Second,
		ManageDNS:        envOr("MANAGE_DNS", "true") != "false",
	}

	if v := os.Getenv("RESYNC_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid RESYNC_INTERVAL %q: %w", v, err)
		}
		cfg.ResyncInterval = d
	}

	var missing []string
	if cfg.APIToken == "" {
		missing = append(missing, "CLOUDFLARE_API_TOKEN")
	}
	if cfg.AccountID == "" {
		missing = append(missing, "CLOUDFLARE_ACCOUNT_ID")
	}
	if cfg.TunnelID == "" {
		missing = append(missing, "TUNNEL_ID")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
