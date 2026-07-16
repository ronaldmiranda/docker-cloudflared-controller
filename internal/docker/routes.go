// Package docker discovers desired tunnel routes from container labels and
// watches the docker daemon for changes.
package docker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Route is one public hostname -> origin service mapping declared by a
// container's labels.
type Route struct {
	Hostname    string
	Path        string
	Service     string // e.g. http://myapp:8080
	NoTLSVerify bool

	// ContainerName is kept for logging only.
	ContainerName string
}

type Discovery struct {
	cli         *client.Client
	labelPrefix string
	log         *slog.Logger
}

func NewDiscovery(labelPrefix string, log *slog.Logger) (*Discovery, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &Discovery{cli: cli, labelPrefix: labelPrefix, log: log}, nil
}

func (d *Discovery) Close() error { return d.cli.Close() }

// DesiredRoutes lists running containers carrying the label prefix and parses
// their route declarations. Invalid declarations are logged and skipped so a
// single bad container never blocks the others.
func (d *Discovery) DesiredRoutes(ctx context.Context) ([]Route, error) {
	f := make(client.Filters).Add("status", "running")
	res, err := d.cli.ContainerList(ctx, client.ContainerListOptions{Filters: f})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var routes []Route
	seen := map[string]string{} // hostname+path -> container, for duplicate detection
	for _, ctr := range res.Items {
		name := containerName(ctr)
		parsed, err := d.parseLabels(name, ctr.Labels, containerIPs(ctr))
		if err != nil {
			d.log.Error("ignoring container with invalid labels", "container", name, "error", err)
			continue
		}
		for _, r := range parsed {
			key := r.Hostname + r.Path
			if other, dup := seen[key]; dup {
				d.log.Error("duplicate route, keeping first",
					"hostname", r.Hostname, "kept", other, "ignored", name)
				continue
			}
			seen[key] = name
			routes = append(routes, r)
		}
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Hostname != routes[j].Hostname {
			return routes[i].Hostname < routes[j].Hostname
		}
		// Rules with a path must come before the catch-all for the same host,
		// since cloudflared evaluates ingress rules in order.
		return routes[i].Path > routes[j].Path
	})
	return routes, nil
}

// parseLabels reads route declarations from one container's labels.
//
// Single route (the common case):
//
//	cloudflared.hostname=app.example.com
//	cloudflared.port=8080
//
// Multiple routes use an index segment:
//
//	cloudflared.0.hostname=web.example.com
//	cloudflared.0.port=80
//	cloudflared.1.hostname=api.example.com
//	cloudflared.1.port=3000
//
// Optional keys per route: service (full origin URL, overrides everything
// else), scheme (default http), target (default container name), network
// (use the container's IP on that docker network instead of its name), path,
// notlsverify.
func (d *Discovery) parseLabels(containerName string, labels map[string]string, networks map[string]string) ([]Route, error) {
	prefix := d.labelPrefix + "."
	groups := map[string]map[string]string{}
	for k, v := range labels {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		group, key := "", rest
		if i := strings.IndexByte(rest, '.'); i >= 0 {
			group, key = rest[:i], rest[i+1:]
		}
		if groups[group] == nil {
			groups[group] = map[string]string{}
		}
		groups[group][key] = v
	}

	var routes []Route
	for group, kv := range groups {
		r, err := buildRoute(containerName, kv, networks)
		if err != nil {
			if group != "" {
				return nil, fmt.Errorf("route %q: %w", group, err)
			}
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, nil
}

func buildRoute(containerName string, kv map[string]string, networks map[string]string) (Route, error) {
	r := Route{
		Hostname:      kv["hostname"],
		Path:          kv["path"],
		NoTLSVerify:   kv["notlsverify"] == "true",
		ContainerName: containerName,
	}
	if r.Hostname == "" {
		return r, fmt.Errorf("missing required label key %q", "hostname")
	}

	if svc := kv["service"]; svc != "" {
		r.Service = svc
		return r, nil
	}

	port := kv["port"]
	if port == "" {
		return r, fmt.Errorf("hostname %q: label key %q (or %q) is required", r.Hostname, "port", "service")
	}
	scheme := kv["scheme"]
	if scheme == "" {
		scheme = "http"
	}
	target := kv["target"]
	if netName := kv["network"]; netName != "" {
		if target != "" {
			return r, fmt.Errorf("hostname %q: label keys %q and %q are mutually exclusive", r.Hostname, "target", "network")
		}
		ip := networks[netName]
		if ip == "" {
			return r, fmt.Errorf("hostname %q: container has no IP on network %q (connected: %v)",
				r.Hostname, netName, sortedKeys(networks))
		}
		target = ip
	}
	if target == "" {
		target = containerName
	}
	r.Service = fmt.Sprintf("%s://%s:%s", scheme, target, port)
	return r, nil
}

// containerIPs maps each docker network the container is attached to onto its
// IP address there.
func containerIPs(ctr container.Summary) map[string]string {
	if ctr.NetworkSettings == nil {
		return nil
	}
	ips := make(map[string]string, len(ctr.NetworkSettings.Networks))
	for name, ep := range ctr.NetworkSettings.Networks {
		if ep == nil || !ep.IPAddress.IsValid() {
			continue
		}
		addr := ep.IPAddress.Unmap()
		ip := addr.String()
		if addr.Is6() {
			ip = "[" + ip + "]" // keep the origin URL valid
		}
		ips[name] = ip
	}
	return ips
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func containerName(ctr container.Summary) string {
	if len(ctr.Names) > 0 {
		return strings.TrimPrefix(ctr.Names[0], "/")
	}
	if len(ctr.ID) >= 12 {
		return ctr.ID[:12]
	}
	return ctr.ID
}
