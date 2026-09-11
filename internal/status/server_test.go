package status

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ronaldmiranda/cloudflared-controller/internal/docker"
)

func TestServePageListsRoutes(t *testing.T) {
	routes := []docker.Route{
		{Hostname: "app.example.com", Service: "http://app:8080", ContainerName: "app"},
	}
	srv := New(":0", func() []docker.Route { return routes }, slog.Default())

	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "app.example.com") {
		t.Fatalf("body missing hostname: %s", body)
	}
}

func TestServePageEscapesHostnames(t *testing.T) {
	routes := []docker.Route{{Hostname: "<script>alert(1)</script>", ContainerName: "evil"}}
	srv := New(":0", func() []docker.Route { return routes }, slog.Default())

	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rec.Result().Body)
	if strings.Contains(string(body), "<script>") {
		t.Fatalf("hostname was not HTML-escaped: %s", body)
	}
}

func TestServeUnknownPathIs404(t *testing.T) {
	srv := New(":0", func() []docker.Route { return nil }, slog.Default())

	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRunShutsDownOnContextCancel(t *testing.T) {
	srv := New("127.0.0.1:0", func() []docker.Route { return nil }, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
