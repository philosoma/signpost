package collect

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// PortalStats summarises the data-fabric registry.
type PortalStats struct {
	Health
	Configured bool `json:"configured"`
	// HasCatalog and HasMachines are false when this portal version does not
	// serve that endpoint, so a missing count is not shown as zero.
	HasCatalog  bool `json:"has_catalog"`
	HasMachines bool `json:"has_machines"`
	Orgs        int  `json:"orgs"`
	Simulated   int  `json:"simulated"`
	Topics      int  `json:"topics"`
	Machines    int  `json:"machines"`
}

type portalCatalog struct {
	Totals struct {
		Orgs   int `json:"orgs"`
		Topics int `json:"topics"`
	} `json:"totals"`
	Orgs []struct {
		Synthetic bool `json:"synthetic"`
	} `json:"orgs"`
}

type portalMachines struct {
	Machines []struct {
		SlotID string `json:"slot_id"`
	} `json:"machines"`
}

func (c *Collector) collectPortal(ctx context.Context) {
	if c.secrets.PortalAPIKey == "" {
		c.mu.Lock()
		c.snap.Portal = &PortalStats{Configured: false}
		c.mu.Unlock()
		return
	}

	s, err := c.fetchPortal(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("portal collection failed", "error", err)
		}
		prev := *c.snap.Portal
		prev.Configured = true
		prev.Error = describe(err)
		c.snap.Portal = &prev
		return
	}
	s.UpdatedAt = time.Now().UTC()
	c.snap.Portal = s
}

// fetchPortal only issues GETs. The key it presents is operator-level, because
// the portal has no read-only tier, so it must stay server-side.
func (c *Collector) fetchPortal(ctx context.Context) (*PortalStats, error) {
	auth := "Bearer " + c.secrets.PortalAPIKey

	s := &PortalStats{Configured: true}

	catURL, err := joinURL(c.cfg.Sources.Portal.URL, "/api/catalog")
	if err != nil {
		return nil, err
	}
	var cat portalCatalog
	switch err := c.getJSON(ctx, catURL, auth, &cat); {
	case err == nil:
		s.HasCatalog = true
		s.Orgs = cat.Totals.Orgs
		s.Topics = cat.Totals.Topics
		for _, o := range cat.Orgs {
			if o.Synthetic {
				s.Simulated++
			}
		}
	case !isNotFound(err):
		return nil, err
	}

	macURL, err := joinURL(c.cfg.Sources.Portal.URL, "/api/machines")
	if err != nil {
		return nil, err
	}
	var mac portalMachines
	switch err := c.getJSON(ctx, macURL, auth, &mac); {
	case err == nil:
		s.HasMachines = true
		s.Machines = len(mac.Machines)
	case !isNotFound(err):
		return nil, err
	}

	if !s.HasCatalog && !s.HasMachines {
		return nil, &statusError{code: http.StatusNotFound}
	}
	return s, nil
}

func isNotFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code == http.StatusNotFound
}
