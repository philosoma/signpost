package collect

import (
	"context"
	"time"
)

// FederationStats summarises the peerings the Federation app manages.
type FederationStats struct {
	Health
	Configured bool             `json:"configured"`
	Live       int              `json:"live"`
	Attention  int              `json:"attention"`
	Setting    int              `json:"setting"`
	Links      []FederationLink `json:"links,omitempty"`
}

// Total is every peering, whatever its state.
func (f *FederationStats) Total() int { return f.Live + f.Attention + f.Setting }

// FederationLink is one peering: who, and live, attention or setup.
type FederationLink struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// federationSummary is Federation's GET /api/summary.
type federationSummary struct {
	Live      int `json:"live"`
	Attention int `json:"attention"`
	Setting   int `json:"setting"`
	Links     []struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	} `json:"links"`
}

func (c *Collector) collectFederation(ctx context.Context) {
	if c.secrets.FederationKey == "" {
		c.mu.Lock()
		c.snap.Federation = &FederationStats{Configured: false}
		c.mu.Unlock()
		return
	}

	s, err := c.fetchFederation(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("federation collection failed", "error", err)
		}
		prev := *c.snap.Federation
		prev.Configured = true
		prev.Error = describe(err)
		c.snap.Federation = &prev
		return
	}
	s.UpdatedAt = time.Now().UTC()
	c.snap.Federation = s
}

func (c *Collector) fetchFederation(ctx context.Context) (*FederationStats, error) {
	u, err := joinURL(c.cfg.Sources.Federation.URL, "/api/summary")
	if err != nil {
		return nil, err
	}
	var sum federationSummary
	if err := c.getJSON(ctx, u, "Bearer "+c.secrets.FederationKey, &sum); err != nil {
		return nil, err
	}
	s := &FederationStats{Configured: true, Live: sum.Live, Attention: sum.Attention, Setting: sum.Setting}
	for _, l := range sum.Links {
		s.Links = append(s.Links, FederationLink{Name: l.Name, State: l.State, Detail: l.Detail})
	}
	return s, nil
}
