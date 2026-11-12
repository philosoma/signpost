// Package config loads and validates Signpost's JSON configuration.
//
// The file holds site details but no secrets. Tokens come from the environment
// (see Secrets) so the file can be mounted read-only and kept in git.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is the whole configuration file.
type Config struct {
	Site           Site      `json:"site"`
	Auth           Auth      `json:"auth"`
	RefreshSeconds int       `json:"refresh_seconds"`
	Sections       []Section `json:"sections"`
	Discover       *Discover `json:"discover,omitempty"`
	Sources        Sources   `json:"sources"`
}

// Discover finds the node's apps from its Traefik route files while Signpost
// runs, so an app appears when its route is added and goes when it is removed,
// with no redeploy. Links in Sections are always shown as well.
type Discover struct {
	// Dir holds the route files, mounted read-only. Signpost reads only the
	// Host(`...`) names from them.
	Dir string `json:"dir"`
	// Domain limits discovery to <prefix>.<domain> hosts.
	Domain string `json:"domain"`
	// CoreAddress is where discovered hosts are reached. Inside a container the
	// mesh names may not resolve, so checks connect here instead. Optional.
	CoreAddress string `json:"core_address,omitempty"`
	// Catalogue names known apps and puts them in sections.
	Catalogue []CatalogueEntry `json:"catalogue"`
	// Hide lists prefixes to leave off the page, such as infrastructure hosts.
	Hide []string `json:"hide,omitempty"`
	// SectionOrder puts these section titles first, in this order.
	SectionOrder []string `json:"section_order,omitempty"`
	// OtherSection holds routed apps the catalogue does not name. Default "Other".
	OtherSection string `json:"other_section,omitempty"`
}

// CatalogueEntry describes the app at <prefix>.<domain>.
type CatalogueEntry struct {
	Section     string `json:"section"`
	Prefix      string `json:"prefix"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Path is added to the link, and the check uses the root instead.
	Path string `json:"path,omitempty"`
	// Probe defaults to on.
	Probe *bool `json:"probe,omitempty"`
	Frame *bool `json:"frame,omitempty"`
}

// Site names the goatnet on the page.
type Site struct {
	Name string `json:"name"`
	// Flag is optional: a country code with a file in static/flags. Only "gb" so far.
	Flag string `json:"flag"`
}

// Auth lists the groups allowed in. At least one is required, so a config that
// omits it cannot produce an open page.
type Auth struct {
	RequiredGroups []string `json:"required_groups"`
}

// Section is a titled group of links.
type Section struct {
	Title string `json:"title"`
	Links []Link `json:"links"`
}

// Link is one destination on the page.
type Link struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	// Probe enables the up/down check. Turn it off for a surface the core
	// cannot reach, so the page says "not checked" rather than "down".
	Probe bool `json:"probe"`
	// ProbeURL replaces URL for the check when the front page is misleading.
	ProbeURL string `json:"probe_url,omitempty"`
	// Frame opens the link inside the page, under the header. It defaults to
	// on for https links. Turn it off for a site that refuses to be framed.
	// Plain http links never open in the frame: browsers block them there.
	Frame *bool `json:"frame,omitempty"`
}

// Framed reports whether the link opens inside the page.
func (l Link) Framed() bool {
	if u, err := url.Parse(l.URL); err != nil || u.Scheme != "https" {
		return false
	}
	return l.Frame == nil || *l.Frame
}

// Sources are the read-only systems stats come from. Any may be left out.
type Sources struct {
	NetBird    *Endpoint    `json:"netbird,omitempty"`
	Portal     *Endpoint    `json:"portal,omitempty"`
	Federation *Endpoint    `json:"federation,omitempty"`
	Prometheus []Prometheus `json:"prometheus,omitempty"`
}

// Endpoint is an API base URL.
type Endpoint struct {
	URL string `json:"url"`
}

// Prometheus is one named Prometheus server.
type Prometheus struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Secrets are read from the environment, never from the config file.
type Secrets struct {
	NetBirdToken  string
	PortalAPIKey  string
	FederationKey string
}

// SecretsFromEnv reads the secrets Signpost understands.
func SecretsFromEnv() Secrets {
	return Secrets{
		NetBirdToken:  strings.TrimSpace(os.Getenv("SIGNPOST_NETBIRD_TOKEN")),
		PortalAPIKey:  strings.TrimSpace(os.Getenv("SIGNPOST_PORTAL_API_KEY")),
		FederationKey: strings.TrimSpace(os.Getenv("SIGNPOST_FEDERATION_KEY")),
	}
}

// Refresh returns the collection interval.
func (c *Config) Refresh() time.Duration {
	return time.Duration(c.RefreshSeconds) * time.Second
}

// Load reads and validates a config file. Unknown fields are errors, so a typo
// in a setting cannot silently switch something off.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var c Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &c, nil
}

// Validate applies defaults and reports every problem at once.
func (c *Config) Validate() error {
	var errs []error
	if c.Site.Flag != "" && c.Site.Flag != "gb" {
		errs = append(errs, fmt.Errorf("site.flag %q is not available; use \"gb\" or leave it out", c.Site.Flag))
	}
	if strings.TrimSpace(c.Site.Name) == "" {
		errs = append(errs, errors.New("site.name is required"))
	}
	if c.RefreshSeconds == 0 {
		c.RefreshSeconds = 30
	}
	if c.RefreshSeconds < 5 {
		errs = append(errs, errors.New("refresh_seconds must be at least 5"))
	}

	groups := 0
	for _, g := range c.Auth.RequiredGroups {
		if strings.TrimSpace(g) != "" {
			groups++
		}
	}
	if groups == 0 {
		errs = append(errs, errors.New("auth.required_groups must name at least one group"))
	}

	for i, s := range c.Sections {
		if strings.TrimSpace(s.Title) == "" {
			errs = append(errs, fmt.Errorf("sections[%d].title is required", i))
		}
		for j, l := range s.Links {
			where := fmt.Sprintf("sections[%d].links[%d]", i, j)
			if strings.TrimSpace(l.Name) == "" {
				errs = append(errs, fmt.Errorf("%s.name is required", where))
			}
			if err := checkURL(l.URL); err != nil {
				errs = append(errs, fmt.Errorf("%s.url: %w", where, err))
			}
			if l.ProbeURL != "" {
				if err := checkURL(l.ProbeURL); err != nil {
					errs = append(errs, fmt.Errorf("%s.probe_url: %w", where, err))
				}
			}
		}
	}

	if d := c.Discover; d != nil {
		if strings.TrimSpace(d.Dir) == "" {
			errs = append(errs, errors.New("discover.dir is required"))
		}
		if strings.TrimSpace(d.Domain) == "" || strings.ContainsAny(d.Domain, "/:@ ") {
			errs = append(errs, errors.New("discover.domain must be a DNS name"))
		}
		if d.CoreAddress != "" && net.ParseIP(d.CoreAddress) == nil {
			errs = append(errs, errors.New("discover.core_address must be an IP address"))
		}
		if d.OtherSection == "" {
			d.OtherSection = "Other"
		}
		for i, e := range d.Catalogue {
			where := fmt.Sprintf("discover.catalogue[%d]", i)
			if strings.TrimSpace(e.Prefix) == "" || strings.ContainsAny(e.Prefix, "/:@ ") {
				errs = append(errs, fmt.Errorf("%s.prefix must be a DNS label", where))
			}
			if strings.TrimSpace(e.Name) == "" {
				errs = append(errs, fmt.Errorf("%s.name is required", where))
			}
			if strings.TrimSpace(e.Section) == "" {
				errs = append(errs, fmt.Errorf("%s.section is required", where))
			}
			if e.Path != "" && !strings.HasPrefix(e.Path, "/") {
				errs = append(errs, fmt.Errorf("%s.path must start with /", where))
			}
		}
	}

	if c.Sources.NetBird != nil {
		if err := checkURL(c.Sources.NetBird.URL); err != nil {
			errs = append(errs, fmt.Errorf("sources.netbird.url: %w", err))
		}
	}
	if c.Sources.Portal != nil {
		if err := checkURL(c.Sources.Portal.URL); err != nil {
			errs = append(errs, fmt.Errorf("sources.portal.url: %w", err))
		}
	}
	if c.Sources.Federation != nil {
		if err := checkURL(c.Sources.Federation.URL); err != nil {
			errs = append(errs, fmt.Errorf("sources.federation.url: %w", err))
		}
	}
	for i, p := range c.Sources.Prometheus {
		if strings.TrimSpace(p.Name) == "" {
			errs = append(errs, fmt.Errorf("sources.prometheus[%d].name is required", i))
		}
		if err := checkURL(p.URL); err != nil {
			errs = append(errs, fmt.Errorf("sources.prometheus[%d].url: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

// checkURL allows only absolute http(s) URLs with no embedded credentials. It
// keeps a javascript: link off the page and a password out of the rendered HTML.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must be http or https")
	}
	if u.Host == "" {
		return errors.New("must include a host")
	}
	if u.User != nil {
		return errors.New("must not contain credentials")
	}
	return nil
}
