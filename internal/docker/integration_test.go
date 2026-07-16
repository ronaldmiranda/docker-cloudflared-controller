//go:build integration

package docker

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// Requires a running docker daemon and a container started with:
//
//	docker run -d --rm --name cfc-inttest \
//	  --label cloudflared.hostname=inttest.example.com \
//	  --label cloudflared.port=8080 alpine sleep 300
//
// Run with: go test -tags integration ./internal/docker
func TestDesiredRoutesIntegration(t *testing.T) {
	d, err := NewDiscovery("cloudflared", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	routes, err := d.DesiredRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.Hostname == "inttest.example.com" && r.Service == "http://cfc-inttest:8080" {
			return
		}
	}
	t.Fatalf("expected route for inttest.example.com, got %+v", routes)
}
