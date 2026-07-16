package docker

import (
	"log/slog"
	"reflect"
	"sort"
	"testing"
)

func testDiscovery() *Discovery {
	return &Discovery{labelPrefix: "cloudflared", log: slog.Default()}
}

func TestParseLabelsSingleRoute(t *testing.T) {
	d := testDiscovery()
	routes, err := d.parseLabels("myapp", map[string]string{
		"cloudflared.hostname": "app.example.com",
		"cloudflared.port":     "8080",
		"com.docker.compose":   "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Route{{
		Hostname:      "app.example.com",
		Service:       "http://myapp:8080",
		ContainerName: "myapp",
	}}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("got %+v, want %+v", routes, want)
	}
}

func TestParseLabelsServiceOverride(t *testing.T) {
	d := testDiscovery()
	routes, err := d.parseLabels("myapp", map[string]string{
		"cloudflared.hostname": "app.example.com",
		"cloudflared.service":  "https://192.168.1.10:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	if routes[0].Service != "https://192.168.1.10:8443" {
		t.Errorf("service = %q", routes[0].Service)
	}
}

func TestParseLabelsSchemeTargetTLS(t *testing.T) {
	d := testDiscovery()
	routes, err := d.parseLabels("myapp", map[string]string{
		"cloudflared.hostname":    "app.example.com",
		"cloudflared.port":        "8443",
		"cloudflared.scheme":      "https",
		"cloudflared.target":      "internal-host",
		"cloudflared.notlsverify": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := routes[0]
	if r.Service != "https://internal-host:8443" {
		t.Errorf("service = %q", r.Service)
	}
	if !r.NoTLSVerify {
		t.Error("expected NoTLSVerify")
	}
}

func TestParseLabelsMultipleRoutes(t *testing.T) {
	d := testDiscovery()
	routes, err := d.parseLabels("myapp", map[string]string{
		"cloudflared.0.hostname": "web.example.com",
		"cloudflared.0.port":     "80",
		"cloudflared.1.hostname": "api.example.com",
		"cloudflared.1.port":     "3000",
		"cloudflared.1.path":     "/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Hostname < routes[j].Hostname })
	want := []Route{
		{Hostname: "api.example.com", Path: "/api", Service: "http://myapp:3000", ContainerName: "myapp"},
		{Hostname: "web.example.com", Service: "http://myapp:80", ContainerName: "myapp"},
	}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("got %+v, want %+v", routes, want)
	}
}

func TestParseLabelsErrors(t *testing.T) {
	d := testDiscovery()

	if _, err := d.parseLabels("myapp", map[string]string{
		"cloudflared.port": "8080",
	}); err == nil {
		t.Error("expected error for missing hostname")
	}

	if _, err := d.parseLabels("myapp", map[string]string{
		"cloudflared.hostname": "app.example.com",
	}); err == nil {
		t.Error("expected error for missing port and service")
	}
}

func TestParseLabelsNoMatch(t *testing.T) {
	d := testDiscovery()
	routes, err := d.parseLabels("myapp", map[string]string{
		"traefik.enable": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Errorf("expected no routes, got %+v", routes)
	}
}
