package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/philosoma/signpost/internal/collect"
	"github.com/philosoma/signpost/internal/config"
)

type fixedSource struct{ snap collect.Snapshot }

func (f fixedSource) Snapshot() collect.Snapshot { return f.snap }

func testConfig() *config.Config {
	return &config.Config{
		Site:           config.Site{Name: "Test Net"},
		Auth:           config.Auth{RequiredGroups: []string{"PLATFORM-ADMIN", "IAM-ADMIN"}},
		RefreshSeconds: 30,
		Sections: []config.Section{{Title: "Administer", Links: []config.Link{
			{Name: "Portal", URL: "https://portal.example", Description: "Registry", Probe: true},
			{Name: "Grafana", URL: "http://192.0.2.1:3000"},
		}}},
	}
}

func handler(t *testing.T, snap collect.Snapshot) http.Handler {
	t.Helper()
	h, err := New(testConfig(), fixedSource{snap}, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(h http.Handler, path string, headers map[string][]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var admin = map[string][]string{"X-Forwarded-Groups": {"PLATFORM-ADMIN"}, "X-Forwarded-Email": {"a@example.com"}}

func TestAccessControl(t *testing.T) {
	h := handler(t, collect.Snapshot{})
	cases := []struct {
		name    string
		headers map[string][]string
		want    int
	}{
		{"no headers", nil, 403},
		{"wrong group", map[string][]string{"X-Forwarded-Groups": {"VIEWER"}}, 403},
		{"partial name", map[string][]string{"X-Forwarded-Groups": {"PLATFORM-ADMINS"}}, 403},
		{"forgeable header ignored", map[string][]string{"X-Auth-Request-Groups": {"PLATFORM-ADMIN"}}, 403},
		{"comma list", map[string][]string{"X-Forwarded-Groups": {"VIEWER, IAM-ADMIN"}}, 200},
		{"repeated header", map[string][]string{"X-Forwarded-Groups": {"VIEWER", "PLATFORM-ADMIN"}}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/", "/api/live", "/api/status", "/static/signpost.css"} {
				if got := get(h, path, tc.headers).Code; got != tc.want {
					t.Errorf("%s: %d, want %d", path, got, tc.want)
				}
			}
		})
	}
}

func TestNewRefusesConfigWithoutGroups(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.RequiredGroups = []string{" "}
	if _, err := New(cfg, fixedSource{}, "t", slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("expected an error")
	}
}

func TestHealthzIsOpenAndRevealsNothing(t *testing.T) {
	rec := get(handler(t, collect.Snapshot{}), "/healthz", nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := get(handler(t, collect.Snapshot{}), "/", admin)
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("CSP missing")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("page may be cached")
	}
}

func TestPageShowsLinksAndStates(t *testing.T) {
	snap := collect.Snapshot{
		GeneratedAt: time.Now(),
		Links: []collect.LinkStatus{
			{State: collect.StateDown, Detail: "connection refused"},
			{State: collect.StateNotChecked},
		},
	}
	body := get(handler(t, snap), "/", admin).Body.String()
	for _, want := range []string{"Test Net", "https://portal.example", "connection refused", "Not checked", "a@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestPageEscapesConfiguredText(t *testing.T) {
	cfg := testConfig()
	cfg.Sections[0].Links[0].Description = `<script>alert(1)</script>`
	h, err := New(cfg, fixedSource{collect.Snapshot{}}, "t", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(h, "/", admin).Body.String(), "<script>alert") {
		t.Fatal("description was not escaped")
	}
}

func TestStaleZeroIsNotShownAsClear(t *testing.T) {
	now := time.Now()
	// Two Prometheus: one fine with no alerts, one failing.
	got := alertsTile([]collect.PromStats{
		{Health: collect.Health{UpdatedAt: now}},
		{Health: collect.Health{Error: "timed out"}},
	})
	if got.Status != "Partial" {
		t.Fatalf("status = %q", got.Status)
	}
	if got := alertsTile([]collect.PromStats{{Health: collect.Health{Error: "timed out"}}}); got.Value != "—" || got.Status != "Unavailable" {
		t.Fatalf("tile = %+v", got)
	}
}

func TestMeshTileStates(t *testing.T) {
	now := time.Now()
	cases := []struct {
		in     *collect.NetBirdStats
		status string
		value  string
	}{
		{&collect.NetBirdStats{Configured: false}, "Off", "—"},
		{&collect.NetBirdStats{Configured: true, Health: collect.Health{Error: "timed out"}}, "Unavailable", "—"},
		{&collect.NetBirdStats{Configured: true, Total: 2, Connected: 2, Health: collect.Health{UpdatedAt: now}}, "All online", "2/2"},
		{&collect.NetBirdStats{Configured: true, Total: 2, Connected: 1, Health: collect.Health{UpdatedAt: now}}, "1 offline", "1/2"},
		{&collect.NetBirdStats{Configured: true, Total: 2, Connected: 2, Health: collect.Health{UpdatedAt: now, Error: "timed out"}}, "Stale", "2/2"},
	}
	for _, tc := range cases {
		got := meshTile(tc.in)
		if got.Status != tc.status || got.Value != tc.value {
			t.Errorf("%+v: got %q %q", tc.in, got.Status, got.Value)
		}
	}
}

func TestSnapshotNeverIncludesSecrets(t *testing.T) {
	cfg := &config.Config{Sources: config.Sources{NetBird: &config.Endpoint{URL: "http://127.0.0.1:1"}, Portal: &config.Endpoint{URL: "http://127.0.0.1:1"}}}
	c := collect.New(cfg, config.Secrets{NetBirdToken: "SECRET-NB", PortalAPIKey: "SECRET-PORTAL"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.Refresh(t.Context())
	h, err := New(testConfig(), c, "t", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/api/live", "/api/status"} {
		if strings.Contains(get(h, path, admin).Body.String(), "SECRET-") {
			t.Errorf("%s exposes a secret", path)
		}
	}
}

func TestLinksOpenInFrameUnlessHTTPOrOff(t *testing.T) {
	off := false
	cfg := testConfig()
	cfg.Sections[0].Links = append(cfg.Sections[0].Links,
		config.Link{Name: "AWS console", URL: "https://aws.example", Frame: &off},
		config.Link{Name: "Portal", URL: "https://portal2.example"},
		config.Link{Name: "Observability", URL: "https://obs.example"},
	)
	h, err := New(cfg, fixedSource{}, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	body := get(h, "/", admin).Body.String()
	for _, want := range []string{
		`href="https://portal.example" data-frame="portal"`,
		`href="https://portal2.example" data-frame="portal-2"`,
		`href="https://obs.example" data-frame="observability-2"`,
		`href="http://192.0.2.1:3000" class="external"`,
		`href="https://aws.example" class="external"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(body, `data-frame="aws`) || strings.Contains(body, `data-frame="grafana`) {
		t.Error("an unframed link has a frame id")
	}
	if !strings.Contains(get(h, "/", admin).Header().Get("Content-Security-Policy"), "frame-src https:") {
		t.Error("CSP must allow https frames")
	}
}

func TestHeaderNamesTheSiteNotGoat(t *testing.T) {
	body := get(handler(t, collect.Snapshot{}), "/", admin).Body.String()
	if !strings.Contains(body, `<span class="brand-name">Test Net</span>`) {
		t.Error("header must show the site name")
	}
	if strings.Contains(strings.ToLower(body), ">goat") {
		t.Error("header still says goat")
	}
}

func TestLiveCarriesTheMenuAndUsesSnapshotSections(t *testing.T) {
	snap := collect.Snapshot{Sections: []config.Section{{Title: "Play", Links: []config.Link{
		{Name: "Found App", URL: "https://found.example/", Probe: true},
	}}}, Links: []collect.LinkStatus{{URL: "https://found.example/", State: collect.StateUp}}}
	h := handler(t, snap)
	for _, path := range []string{"/", "/api/live"} {
		body := get(h, path, admin).Body.String()
		if !strings.Contains(body, `id="sp-menu-next"`) || strings.Count(body, `data-frame="found-app"`) < 2 {
			t.Errorf("%s: menu or link missing", path)
		}
	}
}
