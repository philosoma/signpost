package collect

import (
	"context"
	"sort"
	"time"
)

// maxOfflineListed caps the list on the page. The counts stay complete.
const maxOfflineListed = 20

// NetBirdStats summarises the mesh peers.
type NetBirdStats struct {
	Health
	Configured bool          `json:"configured"`
	Total      int           `json:"total"`
	Connected  int           `json:"connected"`
	Offline    []OfflinePeer `json:"offline,omitempty"`
}

// OfflinePeer names a peer that is not connected.
type OfflinePeer struct {
	Name     string    `json:"name"`
	LastSeen time.Time `json:"last_seen"`
}

type netbirdPeer struct {
	Name      string    `json:"name"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"last_seen"`
}

func summarisePeers(peers []netbirdPeer) NetBirdStats {
	s := NetBirdStats{Configured: true, Total: len(peers)}
	for _, p := range peers {
		if p.Connected {
			s.Connected++
		} else {
			s.Offline = append(s.Offline, OfflinePeer{Name: p.Name, LastSeen: p.LastSeen})
		}
	}
	sort.Slice(s.Offline, func(i, j int) bool { return s.Offline[i].LastSeen.After(s.Offline[j].LastSeen) })
	if len(s.Offline) > maxOfflineListed {
		s.Offline = s.Offline[:maxOfflineListed]
	}
	return s
}

func (c *Collector) collectNetBird(ctx context.Context) {
	if c.secrets.NetBirdToken == "" {
		c.mu.Lock()
		c.snap.NetBird = &NetBirdStats{Configured: false}
		c.mu.Unlock()
		return
	}

	s, err := c.fetchNetBird(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("netbird collection failed", "error", err)
		}
		// Replace rather than edit: Snapshot hands this pointer to readers.
		prev := *c.snap.NetBird
		prev.Configured = true
		prev.Error = describe(err)
		c.snap.NetBird = &prev
		return
	}
	s.UpdatedAt = time.Now().UTC()
	c.snap.NetBird = s
}

func (c *Collector) fetchNetBird(ctx context.Context) (*NetBirdStats, error) {
	u, err := joinURL(c.cfg.Sources.NetBird.URL, "/api/peers")
	if err != nil {
		return nil, err
	}
	var peers []netbirdPeer
	if err := c.getJSON(ctx, u, "Token "+c.secrets.NetBirdToken, &peers); err != nil {
		return nil, err
	}
	s := summarisePeers(peers)
	return &s, nil
}
