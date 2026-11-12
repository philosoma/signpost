package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/philosoma/signpost/internal/config"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestClassify(t *testing.T) {
	cases := map[int]LinkState{
		200: StateUp, 204: StateUp, 302: StateUp, 303: StateUp, 401: StateUp,
		403: StateRefused,
		404: StateDown, 500: StateDown, 502: StateDown, 503: StateDown,
	}
	for code, want := range cases {
		if got := classify(code); got != want {
			t.Errorf("classify(%d) = %s, want %s", code, got, want)
		}
	}
}

func TestProbeDoesNotFollowRedirects(t *testing.T) {
	followed := false
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer login.Close()
	surface := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL, http.StatusFound)
	}))
	defer surface.Close()

	c := New(&config.Config{}, config.Secrets{}, quiet())
	st := c.probe(context.Background(), config.Link{Name: "x", URL: surface.URL, Probe: true})
	if st.State != StateUp {
		t.Fatalf("state = %s", st.State)
	}
	if followed {
		t.Fatal("probe followed the redirect")
	}
}

func TestProbeOffIsNotChecked(t *testing.T) {
	c := New(&config.Config{}, config.Secrets{}, quiet())
	st := c.probe(context.Background(), config.Link{Name: "x", URL: "http://192.0.2.1:3000"})
	if st.State != StateNotChecked {
		t.Fatalf("state = %s", st.State)
	}
}

func TestProbeUsesProbeURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(&config.Config{}, config.Secrets{}, quiet())
	st := c.probe(context.Background(), config.Link{Name: "x", URL: srv.URL, ProbeURL: srv.URL + "/health", Probe: true})
	if st.State != StateUp {
		t.Fatalf("state = %s", st.State)
	}
}

func TestDescribeDoesNotLeakDetail(t *testing.T) {
	err := fmt.Errorf(`Get "https://internal.example/x?token=secret": dial tcp 10.0.0.1:443: connect: connection refused`)
	if got := describe(err); got != "connection refused" {
		t.Fatalf("got %q", got)
	}
	other := fmt.Errorf(`Get "https://internal.example/x?token=secret": something odd`)
	if got := describe(other); strings.Contains(got, "secret") || got != "unreachable" {
		t.Fatalf("got %q", got)
	}
}

func TestNetBird(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodGet || r.URL.Path != "/api/peers" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `[
		  {"name":"core","connected":true,"last_seen":"2026-11-07T10:00:00Z"},
		  {"name":"old","connected":false,"last_seen":"2026-11-01T10:00:00Z"},
		  {"name":"new","connected":false,"last_seen":"2026-11-06T10:00:00Z"}]`)
	}))
	defer srv.Close()

	cfg := &config.Config{Sources: config.Sources{NetBird: &config.Endpoint{URL: srv.URL}}}
	c := New(cfg, config.Secrets{NetBirdToken: "tok"}, quiet())
	c.Refresh(context.Background())
	nb := c.Snapshot().NetBird

	if gotAuth != "Token tok" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !nb.OK() || nb.Total != 3 || nb.Connected != 1 {
		t.Fatalf("stats = %+v", nb)
	}
	if len(nb.Offline) != 2 || nb.Offline[0].Name != "new" {
		t.Fatalf("offline = %+v", nb.Offline)
	}
}

func TestSourceWithoutCredentialIsNotConfiguredAndMakesNoCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call to %s", r.URL.Path)
	}))
	defer srv.Close()
	cfg := &config.Config{Sources: config.Sources{
		NetBird: &config.Endpoint{URL: srv.URL},
		Portal:  &config.Endpoint{URL: srv.URL},
	}}
	c := New(cfg, config.Secrets{}, quiet())
	c.Refresh(context.Background())
	s := c.Snapshot()
	if s.NetBird.Configured || s.Portal.Configured {
		t.Fatalf("snapshot = %+v %+v", s.NetBird, s.Portal)
	}
}

func TestFailureKeepsLastDataAndMarksItStale(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `[{"name":"core","connected":true,"last_seen":"2026-11-07T10:00:00Z"}]`)
	}))
	defer srv.Close()

	cfg := &config.Config{Sources: config.Sources{NetBird: &config.Endpoint{URL: srv.URL}}}
	c := New(cfg, config.Secrets{NetBirdToken: "tok"}, quiet())
	c.Refresh(context.Background())
	first := c.Snapshot().NetBird
	updated := first.UpdatedAt

	fail = true
	time.Sleep(time.Millisecond)
	c.Refresh(context.Background())
	nb := c.Snapshot().NetBird

	if nb.OK() {
		t.Fatal("failed source reported OK")
	}
	if !strings.Contains(nb.Error, "credential rejected") {
		t.Fatalf("error = %q", nb.Error)
	}
	if nb.Total != 1 || !nb.UpdatedAt.Equal(updated) {
		t.Fatalf("last data lost or re-dated: %+v", nb)
	}
	if first.Error != "" {
		t.Fatal("an earlier snapshot was modified in place")
	}
}

func TestPortal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("portal received %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/catalog":
			fmt.Fprint(w, `{"totals":{"orgs":3,"topics":7},"orgs":[{"synthetic":true},{"synthetic":true},{"synthetic":false}]}`)
		case "/api/machines":
			fmt.Fprint(w, `{"machines":[{"slot_id":"a"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{Sources: config.Sources{Portal: &config.Endpoint{URL: srv.URL}}}
	c := New(cfg, config.Secrets{PortalAPIKey: "key"}, quiet())
	c.Refresh(context.Background())
	p := c.Snapshot().Portal
	if !p.OK() || p.Orgs != 3 || p.Simulated != 2 || p.Topics != 7 || p.Machines != 1 {
		t.Fatalf("stats = %+v", p)
	}
}

func promServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result []map[string]any
		row := func(metric map[string]string, v string) map[string]any {
			return map[string]any{"metric": metric, "value": []any{1.0, v}}
		}
		q := r.URL.Query().Get("query")
		switch q {
		case queryUp:
			result = append(result,
				row(map[string]string{"job": "node", "instance": "a:9100"}, "1"),
				row(map[string]string{"job": "chrony", "instance": "b:9123"}, "0"))
		case queryAlerts:
			result = append(result, row(map[string]string{"alertname": "DiskFull", "severity": "critical"}, "1"))
		case queryCPU:
			result = append(result, row(map[string]string{"instance": "a:9100"}, "12.5"))
		case queryMem:
			result = append(result, row(map[string]string{"instance": "a:9100"}, "40"))
		case queryDisk:
			result = append(result, row(map[string]string{"instance": "a:9100"}, "NaN"), row(map[string]string{}, "1"))
		default:
			t.Errorf("unexpected query %q", q)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": result}})
	}))
}

func TestPrometheus(t *testing.T) {
	srv := promServer(t)
	defer srv.Close()
	cfg := &config.Config{Sources: config.Sources{Prometheus: []config.Prometheus{{Name: "Obs 1", URL: srv.URL}}}}
	c := New(cfg, config.Secrets{}, quiet())
	c.Refresh(context.Background())
	p := c.Snapshot().Prometheus[0]

	if !p.OK() || p.Name != "Obs 1" || p.TargetsTotal != 2 || p.TargetsUp != 1 {
		t.Fatalf("stats = %+v", p)
	}
	if len(p.DownTargets) != 1 || p.DownTargets[0] != "chrony b:9123" {
		t.Fatalf("down = %v", p.DownTargets)
	}
	if len(p.Alerts) != 1 || p.Alerts[0].Name != "DiskFull" {
		t.Fatalf("alerts = %+v", p.Alerts)
	}
	if len(p.Hosts) != 1 || p.Hosts[0].CPU != 12.5 || p.Hosts[0].Memory != 40 {
		t.Fatalf("hosts = %+v", p.Hosts)
	}
}

func TestOneBadPrometheusDoesNotAffectTheOther(t *testing.T) {
	good := promServer(t)
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	cfg := &config.Config{Sources: config.Sources{Prometheus: []config.Prometheus{
		{Name: "bad", URL: bad.URL}, {Name: "good", URL: good.URL},
	}}}
	c := New(cfg, config.Secrets{}, quiet())
	c.Refresh(context.Background())
	s := c.Snapshot()
	if s.Prometheus[0].OK() || s.Prometheus[0].Name != "bad" || s.Prometheus[0].Error != "HTTP 502" {
		t.Fatalf("bad = %+v", s.Prometheus[0])
	}
	if !s.Prometheus[1].OK() {
		t.Fatalf("good = %+v", s.Prometheus[1])
	}
}

func TestPortalWithoutCatalogStillReportsMachines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/machines" {
			fmt.Fprint(w, `{"machines":[{"slot_id":"a"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cfg := &config.Config{Sources: config.Sources{Portal: &config.Endpoint{URL: srv.URL}}}
	c := New(cfg, config.Secrets{PortalAPIKey: "key"}, quiet())
	c.Refresh(context.Background())
	p := c.Snapshot().Portal
	if !p.OK() || p.HasCatalog || !p.HasMachines || p.Machines != 1 {
		t.Fatalf("stats = %+v", p)
	}
}

func TestPortalWithNeitherEndpointIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cfg := &config.Config{Sources: config.Sources{Portal: &config.Endpoint{URL: srv.URL}}}
	c := New(cfg, config.Secrets{PortalAPIKey: "key"}, quiet())
	c.Refresh(context.Background())
	if p := c.Snapshot().Portal; p.OK() || p.Error != "HTTP 404" {
		t.Fatalf("stats = %+v", p)
	}
}

func TestFederation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/summary" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"checked":"2026-11-12T20:00:00Z","errors":0,"live":1,"attention":0,"setting":1,"links":[{"them":"lva","name":"Latvia","state":"live","detail":"Up"},{"them":"bgr","name":"Bulgaria","state":"setup","detail":"In setup: step 3 of 9"}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Sources: config.Sources{Federation: &config.Endpoint{URL: srv.URL}}}
	c := New(cfg, config.Secrets{FederationKey: "fk"}, quiet())
	c.Refresh(context.Background())
	f := c.Snapshot().Federation
	if !f.OK() || f.Live != 1 || f.Setting != 1 || f.Total() != 2 || len(f.Links) != 2 || f.Links[1].Name != "Bulgaria" || f.Links[1].State != "setup" {
		t.Fatalf("stats = %+v", f)
	}

	// A wrong key keeps the last counts and says why.
	c2 := New(cfg, config.Secrets{FederationKey: "wrong"}, quiet())
	c2.Refresh(context.Background())
	if f := c2.Snapshot().Federation; f.Error == "" || !f.Configured {
		t.Fatalf("wrong key: %+v", f)
	}
	// No key: configured as a source, but off.
	c3 := New(cfg, config.Secrets{}, quiet())
	c3.Refresh(context.Background())
	if f := c3.Snapshot().Federation; f.Configured {
		t.Fatalf("no key: %+v", f)
	}
}
