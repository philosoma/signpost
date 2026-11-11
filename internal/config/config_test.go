package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() *Config {
	return &Config{
		Site: Site{Name: "Test"},
		Auth: Auth{RequiredGroups: []string{"PLATFORM-ADMIN"}},
		Sections: []Section{{Title: "Admin", Links: []Link{
			{Name: "Portal", URL: "https://portal.example", Probe: true},
		}}},
	}
}

func TestValidateAcceptsMinimalConfigAndDefaultsRefresh(t *testing.T) {
	c := valid()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.RefreshSeconds != 30 {
		t.Fatalf("refresh default = %d", c.RefreshSeconds)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"no groups":       {func(c *Config) { c.Auth.RequiredGroups = nil }, "required_groups"},
		"blank group":     {func(c *Config) { c.Auth.RequiredGroups = []string{" "} }, "required_groups"},
		"no name":         {func(c *Config) { c.Site.Name = "" }, "site.name"},
		"unknown flag":    {func(c *Config) { c.Site.Flag = "../x" }, "site.flag"},
		"fast refresh":    {func(c *Config) { c.RefreshSeconds = 1 }, "refresh_seconds"},
		"javascript link": {func(c *Config) { c.Sections[0].Links[0].URL = "javascript:alert(1)" }, "http or https"},
		"relative link":   {func(c *Config) { c.Sections[0].Links[0].URL = "/portal" }, "http or https"},
		"credentials":     {func(c *Config) { c.Sections[0].Links[0].URL = "https://a:b@portal.example" }, "credentials"},
		"bad prometheus":  {func(c *Config) { c.Sources.Prometheus = []Prometheus{{Name: "x", URL: "ftp://x"}} }, "prometheus"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid()
			tc.mutate(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	body := `{"site":{"name":"x"},"auth":{"required_groups":["A"]},"oops":true}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "oops") {
		t.Fatalf("err = %v", err)
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	if _, err := Load("../../examples/config.example.json"); err != nil {
		t.Fatal(err)
	}
}

func TestFramed(t *testing.T) {
	off, on := false, true
	cases := []struct {
		link Link
		want bool
	}{
		{Link{URL: "https://a.example"}, true},
		{Link{URL: "https://a.example", Frame: &off}, false},
		{Link{URL: "http://a.example"}, false},
		{Link{URL: "http://a.example", Frame: &on}, false},
	}
	for _, c := range cases {
		if got := c.link.Framed(); got != c.want {
			t.Errorf("%s frame=%v: got %v", c.link.URL, c.link.Frame, got)
		}
	}
}

func TestValidateDiscover(t *testing.T) {
	ok := valid()
	ok.Discover = &Discover{Dir: "/routes", Domain: "site.example", CoreAddress: "100.64.0.1",
		Catalogue: []CatalogueEntry{{Section: "Play", Prefix: "sentry", Name: "Sentry", Path: "/x"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Discover.OtherSection != "Other" {
		t.Fatalf("other section default = %q", ok.Discover.OtherSection)
	}
	cases := map[string]struct {
		mutate func(*Discover)
		want   string
	}{
		"no dir":        {func(d *Discover) { d.Dir = "" }, "discover.dir"},
		"bad domain":    {func(d *Discover) { d.Domain = "https://x" }, "discover.domain"},
		"bad address":   {func(d *Discover) { d.CoreAddress = "core.example" }, "core_address"},
		"no prefix":     {func(d *Discover) { d.Catalogue[0].Prefix = "" }, "prefix"},
		"no name":       {func(d *Discover) { d.Catalogue[0].Name = "" }, "name"},
		"no section":    {func(d *Discover) { d.Catalogue[0].Section = "" }, "section"},
		"relative path": {func(d *Discover) { d.Catalogue[0].Path = "x" }, "path"},
	}
	for name, tc := range cases {
		c := valid()
		c.Discover = &Discover{Dir: "/routes", Domain: "site.example",
			Catalogue: []CatalogueEntry{{Section: "Play", Prefix: "sentry", Name: "Sentry"}}}
		tc.mutate(c.Discover)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
