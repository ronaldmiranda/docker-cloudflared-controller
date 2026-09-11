// Package status serves a minimal HTML page listing the hostnames currently
// synced to the Cloudflare tunnel, for quick manual inspection.
package status

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/ronaldmiranda/cloudflared-controller/internal/docker"
)

type Server struct {
	http *http.Server
	log  *slog.Logger
}

// New builds a status server listening on addr. routes is called on every
// request, so it should be cheap and safe for concurrent use (e.g.
// (*controller.Controller).Routes).
func New(addr string, routes func() []docker.Route, log *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pageTmpl.Execute(w, routes()); err != nil {
			log.Error("rendering status page", "error", err)
		}
	})
	return &Server{http: &http.Server{Addr: addr, Handler: mux}, log: log}
}

// Run blocks, serving until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	s.log.Info("status page listening", "addr", s.http.Addr)

	errCh := make(chan error, 1)
	go func() { errCh <- s.http.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

var pageTmpl = template.Must(template.New("status").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>cloudflared-controller</title>
<style>
body { font-family: system-ui, sans-serif; margin: 2rem; color: #222; }
table { border-collapse: collapse; width: 100%; max-width: 900px; }
th, td { border: 1px solid #ccc; padding: .4rem .6rem; text-align: left; }
th { background: #f4f4f4; }
</style>
</head>
<body>
<h1>Synced hostnames</h1>
{{if .}}
<table>
<tr><th>Hostname</th><th>Path</th><th>Service</th><th>Container</th></tr>
{{range .}}
<tr><td>{{.Hostname}}</td><td>{{.Path}}</td><td>{{.Service}}</td><td>{{.ContainerName}}</td></tr>
{{end}}
</table>
{{else}}
<p>No routes discovered yet.</p>
{{end}}
</body>
</html>
`))
