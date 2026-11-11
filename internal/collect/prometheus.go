package collect

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/philosoma/signpost/internal/config"
)

// Queries are fixed in code. Viewers cannot supply PromQL.
const (
	queryUp     = `up`
	queryAlerts = `ALERTS{alertstate="firing",alertname!="Watchdog"}`
	queryCPU    = `100 - avg by (instance)(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100`
	queryMem    = `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
	queryDisk   = `100 * (1 - node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"})`
)

// PromStats summarises one Prometheus server.
type PromStats struct {
	Health
	Name         string   `json:"name"`
	TargetsUp    int      `json:"targets_up"`
	TargetsTotal int      `json:"targets_total"`
	DownTargets  []string `json:"down_targets,omitempty"`
	Alerts       []Alert  `json:"alerts,omitempty"`
	Hosts        []Host   `json:"hosts,omitempty"`
}

// Alert is one firing alert.
type Alert struct {
	Name     string `json:"name"`
	Severity string `json:"severity,omitempty"`
}

// Host holds node_exporter usage. Hosts without node_exporter do not appear.
type Host struct {
	Instance string  `json:"instance"`
	CPU      float64 `json:"cpu_percent"`
	Memory   float64 `json:"memory_percent"`
	Disk     float64 `json:"disk_percent"`
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []promSample `json:"result"`
	} `json:"data"`
}

func (s promSample) number() (float64, bool) {
	if len(s.Value) != 2 {
		return 0, false
	}
	str, ok := s.Value[1].(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func (c *Collector) promQuery(ctx context.Context, base, query string) ([]promSample, error) {
	u, err := joinURL(base, "/api/v1/query")
	if err != nil {
		return nil, err
	}
	var r promResponse
	if err := c.getJSON(ctx, u+"?query="+url.QueryEscape(query), "", &r); err != nil {
		return nil, err
	}
	if r.Status != "success" {
		return nil, errors.New("unexpected response: status " + r.Status)
	}
	return r.Data.Result, nil
}

func (c *Collector) collectPrometheus(ctx context.Context, idx int, src config.Prometheus) {
	s, err := c.fetchProm(ctx, src)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("prometheus collection failed", "source", src.Name, "error", err)
		}
		c.snap.Prometheus[idx].Name = src.Name
		c.snap.Prometheus[idx].Error = describe(err)
		return
	}
	s.UpdatedAt = time.Now().UTC()
	c.snap.Prometheus[idx] = *s
}

func (c *Collector) fetchProm(ctx context.Context, src config.Prometheus) (*PromStats, error) {
	s := &PromStats{Name: src.Name}

	up, err := c.promQuery(ctx, src.URL, queryUp)
	if err != nil {
		return nil, err
	}
	s.TargetsTotal = len(up)
	for _, r := range up {
		if v, ok := r.number(); ok && v == 1 {
			s.TargetsUp++
		} else {
			s.DownTargets = append(s.DownTargets, r.Metric["job"]+" "+r.Metric["instance"])
		}
	}
	sort.Strings(s.DownTargets)

	alerts, err := c.promQuery(ctx, src.URL, queryAlerts)
	if err != nil {
		return nil, err
	}
	for _, r := range alerts {
		s.Alerts = append(s.Alerts, Alert{Name: r.Metric["alertname"], Severity: r.Metric["severity"]})
	}
	sort.Slice(s.Alerts, func(i, j int) bool { return s.Alerts[i].Name < s.Alerts[j].Name })

	hosts := map[string]*Host{}
	for _, m := range []struct {
		query string
		set   func(*Host, float64)
	}{
		{queryCPU, func(h *Host, v float64) { h.CPU = v }},
		{queryMem, func(h *Host, v float64) { h.Memory = v }},
		{queryDisk, func(h *Host, v float64) { h.Disk = v }},
	} {
		rows, err := c.promQuery(ctx, src.URL, m.query)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			inst := r.Metric["instance"]
			v, ok := r.number()
			if inst == "" || !ok {
				continue
			}
			if hosts[inst] == nil {
				hosts[inst] = &Host{Instance: inst}
			}
			m.set(hosts[inst], v)
		}
	}
	for _, h := range hosts {
		s.Hosts = append(s.Hosts, *h)
	}
	sort.Slice(s.Hosts, func(i, j int) bool { return s.Hosts[i].Instance < s.Hosts[j].Instance })
	return s, nil
}
