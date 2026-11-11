package collect

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/philosoma/signpost/internal/config"
)

func writeRoute(t *testing.T, dir, name, host string) {
	t.Helper()
	body := "http:\n  routers:\n    r:\n      rule: \"Host(`" + host + "`) && PathPrefix(`/x`)\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func discoverConfig(dir string) *config.Config {
	off := false
	cfg := &config.Config{
		Site: config.Site{Name: "t"},
		Auth: config.Auth{RequiredGroups: []string{"A"}},
		Sections: []config.Section{
			{Title: "Observe", Links: []config.Link{{Name: "Grafana", URL: "http://192.0.2.1:3000/"}}},
			{Title: "Cloud", Links: []config.Link{{Name: "AWS", URL: "https://aws.example/", Frame: &off}}},
		},
		Discover: &config.Discover{
			Dir:          dir,
			Domain:       "site.example",
			Hide:         []string{"mgmt"},
			SectionOrder: []string{"Administer", "Observe", "Play"},
			Catalogue: []config.CatalogueEntry{
				{Section: "Administer", Prefix: "auth", Name: "Account", Path: "/settings"},
				{Section: "Observe", Prefix: "panoscope", Name: "Panoscope"},
				{Section: "Play", Prefix: "sentry", Name: "Sentry"},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return cfg
}

func titlesAndNames(sections []config.Section) []string {
	var out []string
	for _, s := range sections {
		names := []string{}
		for _, l := range s.Links {
			names = append(names, l.Name)
		}
		out = append(out, s.Title+": "+strings.Join(names, ", "))
	}
	return out
}

func TestRoutedHostsReadsNamesOnly(t *testing.T) {
	dir := t.TempDir()
	writeRoute(t, dir, "a.yml", "Panoscope.site.example")
	writeRoute(t, dir, "b.yaml", "{{ .Host }}")
	writeRoute(t, dir, "c.txt", "ignored.site.example")
	if err := os.Mkdir(filepath.Join(dir, "sub.yml"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := routedHosts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"panoscope.site.example"}) {
		t.Fatalf("hosts = %v", got)
	}
}

func TestLayoutShowsRoutedAppsAndOthers(t *testing.T) {
	cfg := discoverConfig(t.TempDir())
	hosts := []string{
		"auth.site.example", "sentry.site.example", "mgmt.site.example",
		"get.site.example", "air-defenders.site.example", "join.other.example",
	}
	got := titlesAndNames(layout(cfg, hosts))
	want := []string{
		"Administer: Account",
		"Observe: Grafana",
		"Play: Sentry",
		"Cloud: AWS",
		"Other: Air Defenders, Get",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("layout =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	account := layout(cfg, hosts)[0].Links[0]
	if account.URL != "https://auth.site.example/settings" || account.ProbeURL != "https://auth.site.example/" || !account.Probe {
		t.Fatalf("account link = %+v", account)
	}
}

func TestAppsComeAndGoWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := discoverConfig(dir)
	writeRoute(t, dir, "panoscope.yml", "panoscope.site.example")
	c := New(cfg, config.Secrets{}, quiet())

	names := func() []string { return titlesAndNames(c.Snapshot().Sections) }
	if got := names(); !slices.Contains(got, "Observe: Panoscope, Grafana") || slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "Play") }) {
		t.Fatalf("at start: %v", got)
	}

	writeRoute(t, dir, "sentry.yml", "sentry.site.example")
	if err := os.Remove(filepath.Join(dir, "panoscope.yml")); err != nil {
		t.Fatal(err)
	}
	c.relayout()
	got := names()
	if !slices.Contains(got, "Play: Sentry") || !slices.Contains(got, "Observe: Grafana") {
		t.Fatalf("after change: %v", got)
	}
	snap := c.Snapshot()
	if n := 0; true {
		for _, s := range snap.Sections {
			n += len(s.Links)
		}
		if n != len(snap.Links) {
			t.Fatalf("%d links but %d statuses", n, len(snap.Links))
		}
	}
	if snap.Discovery == nil || !snap.Discovery.OK() {
		t.Fatalf("discovery health = %+v", snap.Discovery)
	}
}

func TestUnreadableRoutesKeepLastList(t *testing.T) {
	dir := t.TempDir()
	cfg := discoverConfig(dir)
	writeRoute(t, dir, "sentry.yml", "sentry.site.example")
	c := New(cfg, config.Secrets{}, quiet())
	cfg.Discover.Dir = filepath.Join(dir, "gone")
	c.relayout()
	snap := c.Snapshot()
	if !slices.Contains(titlesAndNames(snap.Sections), "Play: Sentry") {
		t.Fatalf("lost the list: %v", titlesAndNames(snap.Sections))
	}
	if snap.Discovery.Error == "" || snap.Discovery.UpdatedAt.IsZero() {
		t.Fatalf("health = %+v", snap.Discovery)
	}
}

func TestLinkKeepsStatusAcrossRelayout(t *testing.T) {
	dir := t.TempDir()
	cfg := discoverConfig(dir)
	writeRoute(t, dir, "sentry.yml", "sentry.site.example")
	c := New(cfg, config.Secrets{}, quiet())
	c.mu.Lock()
	for i := range c.snap.Links {
		c.snap.Links[i].State = StateUp
	}
	c.mu.Unlock()
	writeRoute(t, dir, "panoscope.yml", "panoscope.site.example")
	c.relayout()
	for i, st := range c.Snapshot().Links {
		want := StateUp
		if strings.Contains(st.URL, "panoscope") {
			want = StatePending
		}
		if strings.Contains(st.URL, "192.0.2.1") {
			want = StateUp // was already there
		}
		if st.State != want {
			t.Errorf("link %d %s = %s, want %s", i, st.URL, st.State, want)
		}
	}
}

func TestCoreDialerSendsSiteHostsToCore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "sentry.site.example:") {
			t.Errorf("host header = %q", r.Host)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	host, port, _ := net.SplitHostPort(u.Host)

	client := &http.Client{Transport: &http.Transport{DialContext: coreDialer("site.example", host)}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://sentry.site.example:"+port+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
