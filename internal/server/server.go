// Package server renders the Signpost page.
package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/philosoma/signpost/internal/collect"
	"github.com/philosoma/signpost/internal/config"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Source supplies the current stats.
type Source interface {
	Snapshot() collect.Snapshot
}

type server struct {
	cfg     *config.Config
	src     Source
	version string
	log     *slog.Logger
	tmpl    *template.Template
	groups  []string
}

// New builds the HTTP handler.
func New(cfg *config.Config, src Source, version string, log *slog.Logger) (http.Handler, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago":        ago,
		"pct":        func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
		"chip":       chipClass,
		"stateLabel": stateLabel,
		"fedChip":    fedChip,
		"fedLabel":   fedLabel,
		"meter":      meterClass,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &server{cfg: cfg, src: src, version: version, log: log, tmpl: tmpl}
	for _, g := range cfg.Auth.RequiredGroups {
		if g = strings.TrimSpace(g); g != "" {
			s.groups = append(s.groups, g)
		}
	}
	if len(s.groups) == 0 {
		return nil, fmt.Errorf("no required groups configured")
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}

	guarded := http.NewServeMux()
	guarded.HandleFunc("GET /{$}", s.handlePage)
	guarded.HandleFunc("GET /api/live", s.handleLive)
	guarded.HandleFunc("GET /api/status", s.handleStatus)
	guarded.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))

	root := http.NewServeMux()
	// Liveness only: no stats and no identity, because it bypasses the login.
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	root.Handle("/", s.requireGroup(guarded))
	return securityHeaders(root), nil
}

// requireGroup is a second check behind the oauth2-proxy door, and fails closed:
// a request that did not come through the door has no groups and is refused.
//
// Only X-Forwarded-Groups may be read. oauth2-proxy strips that family from
// client input and sets it from the validated session, while X-Auth-Request-*
// can be forged by the client.
func (s *server) requireGroup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowed(r) {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "You do not have access to Signpost.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) allowed(r *http.Request) bool {
	for _, line := range r.Header.Values("X-Forwarded-Groups") {
		for _, g := range strings.Split(line, ",") {
			if slices.Contains(s.groups, strings.TrimSpace(g)) {
				return true
			}
		}
	}
	return false
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self'; frame-src https:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Revalidate each time: a private page gains nothing from stale CSS.
		w.Header().Set("Cache-Control", "private, no-cache")
		next.ServeHTTP(w, r)
	})
}

func (s *server) handlePage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		Site:           s.cfg.Site.Name,
		Flag:           s.cfg.Site.Flag,
		Viewer:         r.Header.Get("X-Forwarded-Email"),
		Version:        s.version,
		RefreshSeconds: s.cfg.RefreshSeconds,
		Live:           buildLive(s.cfg, s.src.Snapshot(), time.Now()),
	}
	s.render(w, "page.html", data)
}

// handleLive returns the refreshed part of the page as HTML, so there is one
// rendering of the stats rather than one in Go and another in JavaScript.
func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "live", buildLive(s.cfg, s.src.Snapshot(), time.Now()))
}

func (s *server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(s.src.Snapshot()); err != nil {
		s.log.Warn("write status", "error", err)
	}
}

func (s *server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render", "template", name, "error", err)
		http.Error(w, "Could not render the page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}
