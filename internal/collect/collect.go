// Package collect gathers Signpost's stats in the background, so page loads
// never wait on, or add load to, the systems being observed.
//
// A source that fails keeps its last good data and is marked with the failure
// and the time of that data. A stale number is never presented as current, and
// an outage is never presented as a zero.
package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/philosoma/signpost/internal/config"
)

// Response bodies are capped so a misbehaving upstream cannot exhaust memory.
const maxBody = 16 << 20

// Snapshot is everything the page shows.
type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Sections is the page's current links. With discovery on it changes as
	// routes come and go. Links holds their statuses in the same order.
	Sections []config.Section `json:"sections"`
	Links    []LinkStatus     `json:"links"`
	// Discovery reports the last read of the route files, when discovery is on.
	Discovery  *Health       `json:"discovery,omitempty"`
	NetBird    *NetBirdStats `json:"netbird,omitempty"`
	Portal     *PortalStats  `json:"portal,omitempty"`
	Prometheus []PromStats   `json:"prometheus"`
}

// Health is the freshness of one source.
type Health struct {
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// OK reports whether the most recent collection succeeded.
func (h Health) OK() bool { return h.Error == "" && !h.UpdatedAt.IsZero() }

// Collector owns the current snapshot.
type Collector struct {
	cfg     *config.Config
	secrets config.Secrets
	client  *http.Client
	log     *slog.Logger

	mu   sync.RWMutex
	snap Snapshot
	// hosts is the last good read of the route files.
	hosts []string
}

// New returns a collector. Nothing is fetched until Run or Refresh is called.
func New(cfg *config.Config, secrets config.Secrets, log *slog.Logger) *Collector {
	c := &Collector{
		cfg:     cfg,
		secrets: secrets,
		log:     log,
		client: &http.Client{
			Timeout: 8 * time.Second,
			// A redirect is an answer to record, not one to follow. Following
			// it would grade the login page instead of the surface.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if d := cfg.Discover; d != nil {
		c.snap.Discovery = &Health{}
		if d.CoreAddress != "" {
			c.client.Transport = &http.Transport{
				DialContext:         coreDialer(d.Domain, d.CoreAddress),
				TLSHandshakeTimeout: 5 * time.Second,
				MaxIdleConns:        16,
				IdleConnTimeout:     60 * time.Second,
			}
		}
	}
	// Read the routes now, so the first page already lists the node's apps.
	c.relayout()
	if cfg.Sources.NetBird != nil {
		c.snap.NetBird = &NetBirdStats{}
	}
	if cfg.Sources.Portal != nil {
		c.snap.Portal = &PortalStats{}
	}
	for _, p := range cfg.Sources.Prometheus {
		c.snap.Prometheus = append(c.snap.Prometheus, PromStats{Name: p.Name})
	}
	return c
}

// Snapshot returns a copy that is safe to read while collection continues.
func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.snap
	s.Links = append([]LinkStatus(nil), c.snap.Links...)
	s.Sections = append([]config.Section(nil), c.snap.Sections...)
	if c.snap.Discovery != nil {
		h := *c.snap.Discovery
		s.Discovery = &h
	}
	s.Prometheus = append([]PromStats(nil), c.snap.Prometheus...)
	return s
}

// Run refreshes on the configured interval until ctx ends.
func (c *Collector) Run(ctx context.Context) {
	c.Refresh(ctx)
	t := time.NewTicker(c.cfg.Refresh())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Refresh(ctx)
		}
	}
}

// Refresh collects every source once, concurrently.
func (c *Collector) Refresh(ctx context.Context) {
	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}

	sections := c.relayout()
	i := 0
	for _, s := range sections {
		for _, l := range s.Links {
			idx, link := i, l
			i++
			run(func() {
				st := c.probe(ctx, link)
				c.mu.Lock()
				c.snap.Links[idx] = st
				c.mu.Unlock()
			})
		}
	}
	if c.cfg.Sources.NetBird != nil {
		run(func() { c.collectNetBird(ctx) })
	}
	if c.cfg.Sources.Portal != nil {
		run(func() { c.collectPortal(ctx) })
	}
	for i, p := range c.cfg.Sources.Prometheus {
		idx, src := i, p
		run(func() { c.collectPrometheus(ctx, idx, src) })
	}
	wg.Wait()

	c.mu.Lock()
	c.snap.GeneratedAt = time.Now().UTC()
	c.mu.Unlock()
}

// relayout rereads the route files, if discovery is on, and sets the page's
// links. A link that stays keeps its last status, so the page does not flash
// "Checking" on every refresh. If the files cannot be read, the last good list
// is kept and the failure is recorded. Only Refresh and New call it, one at a time.
func (c *Collector) relayout() []config.Section {
	var health *Health
	if d := c.cfg.Discover; d != nil {
		hosts, err := routedHosts(d.Dir)
		h := Health{}
		c.mu.RLock()
		if c.snap.Discovery != nil {
			h = *c.snap.Discovery
		}
		c.mu.RUnlock()
		if err != nil {
			c.log.Warn("read routes", "dir", d.Dir, "error", err)
			h.Error = "route files unreadable"
		} else {
			c.hosts = hosts
			h.Error, h.UpdatedAt = "", time.Now().UTC()
		}
		health = &h
	}
	sections := layout(c.cfg, c.hosts)

	c.mu.Lock()
	defer c.mu.Unlock()
	prev := map[string]LinkStatus{}
	for _, st := range c.snap.Links {
		prev[st.URL] = st
	}
	links := []LinkStatus{}
	for _, s := range sections {
		for _, l := range s.Links {
			st, ok := prev[l.URL]
			if !ok {
				st = LinkStatus{URL: l.URL, State: statusFor(l)}
			}
			links = append(links, st)
		}
	}
	c.snap.Sections = sections
	c.snap.Links = links
	if health != nil {
		c.snap.Discovery = health
	}
	return sections
}

// getJSON fetches a URL and decodes a JSON body. The header carries a credential
// and is never logged or included in an error.
func (c *Collector) getJSON(ctx context.Context, rawURL, authHeader string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "signpost")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &statusError{code: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("unexpected response: %w", err)
	}
	return nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

// describe turns an error into text that is safe for the page. Raw errors can
// carry internal detail; those go to the log.
func describe(err error) string {
	var se *statusError
	if errors.As(err, &se) {
		switch se.code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("credential rejected (HTTP %d)", se.code)
		default:
			return fmt.Sprintf("HTTP %d", se.code)
		}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "name not found"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls:"):
		return "TLS error"
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"):
		return "no route"
	case strings.Contains(msg, "unexpected response"):
		return "unexpected response"
	}
	return "unreachable"
}

func joinURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return u.JoinPath(path).String(), nil
}
