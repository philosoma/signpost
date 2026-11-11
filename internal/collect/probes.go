package collect

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/philosoma/signpost/internal/config"
)

// LinkState is the result of checking one link.
type LinkState string

const (
	StateUp         LinkState = "up"
	StateDown       LinkState = "down"
	StateRefused    LinkState = "refused"     // answered, but refused us
	StateNotChecked LinkState = "not_checked" // probing is off for this link
	StatePending    LinkState = "pending"     // no result yet
)

// LinkStatus is what the page shows beside a link.
type LinkStatus struct {
	URL       string    `json:"url"`
	State     LinkState `json:"state"`
	Detail    string    `json:"detail,omitempty"`
	LatencyMS int64     `json:"latency_ms,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
}

func statusFor(l config.Link) LinkState {
	if !l.Probe {
		return StateNotChecked
	}
	return StatePending
}

// classify grades an HTTP status.
//
//   - 2xx and 3xx: up. A surface behind a login answers with a redirect.
//   - 401: up. It is alive and asking for credentials.
//   - 403: refused. It may be fine, but this check was blocked (for example by
//     the mesh allowlist), so it must not be reported as healthy.
//   - 404: down. Traefik answers 404 when no route matches, which is what a
//     removed surface looks like. Use probe_url if the root really is a 404.
//   - anything else: down.
func classify(code int) LinkState {
	switch {
	case code >= 200 && code < 400, code == http.StatusUnauthorized:
		return StateUp
	case code == http.StatusForbidden:
		return StateRefused
	default:
		return StateDown
	}
}

func (c *Collector) probe(ctx context.Context, l config.Link) LinkStatus {
	st := LinkStatus{URL: l.URL, State: StateNotChecked}
	if !l.Probe {
		return st
	}
	target := l.URL
	if l.ProbeURL != "" {
		target = l.ProbeURL
	}
	st.CheckedAt = time.Now().UTC()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		st.State, st.Detail = StateDown, "bad URL"
		return st
	}
	req.Header.Set("User-Agent", "signpost")

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("link check failed", "link", l.Name, "error", err)
		}
		st.State, st.Detail = StateDown, describe(err)
		return st
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	st.LatencyMS = time.Since(start).Milliseconds()
	st.State = classify(resp.StatusCode)
	if st.State != StateUp {
		st.Detail = http.StatusText(resp.StatusCode)
		if st.Detail == "" {
			st.Detail = "unexpected status"
		}
	}
	return st
}
