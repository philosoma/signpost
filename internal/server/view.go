package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/philosoma/signpost/internal/collect"
	"github.com/philosoma/signpost/internal/config"
)

type pageData struct {
	Site           string
	Flag           string
	Viewer         string
	Version        string
	RefreshSeconds int
	Live           liveData
}

type liveData struct {
	Now        time.Time
	Started    bool
	Tiles      []tile
	Sections   []sectionView
	NetBird    *collect.NetBirdStats
	Portal     *collect.PortalStats
	Federation *collect.FederationStats
	Prom       []collect.PromStats
	Hosts      []hostRow
	Alerts     []alertRow
}

type tile struct {
	Label  string
	Value  string
	Detail string
	Chip   string // ok, warn, err, muted
	Status string
}

type sectionView struct {
	Title string
	Links []linkView
}

type linkView struct {
	Name, URL, Description string
	// ID names the link in the address bar (#id) while it is open in the frame.
	// Empty when the link opens normally.
	ID     string
	Status collect.LinkStatus
}

type hostRow struct {
	Source string
	collect.Host
}

type alertRow struct {
	Source string
	collect.Alert
}

func buildLive(cfg *config.Config, snap collect.Snapshot, now time.Time) liveData {
	d := liveData{
		Now:        now,
		Started:    !snap.GeneratedAt.IsZero(),
		NetBird:    snap.NetBird,
		Federation: snap.Federation,
		Portal:     snap.Portal,
		Prom:       snap.Prometheus,
	}

	sections := snap.Sections
	if sections == nil {
		sections = cfg.Sections
	}
	i := 0
	ids := linkIDs(sections)
	for _, s := range sections {
		sv := sectionView{Title: s.Title}
		for _, l := range s.Links {
			lv := linkView{Name: l.Name, URL: l.URL, Description: l.Description, ID: ids[i]}
			if i < len(snap.Links) {
				lv.Status = snap.Links[i]
			}
			i++
			sv.Links = append(sv.Links, lv)
		}
		d.Sections = append(d.Sections, sv)
	}

	for _, p := range snap.Prometheus {
		for _, h := range p.Hosts {
			d.Hosts = append(d.Hosts, hostRow{Source: p.Name, Host: h})
		}
		for _, a := range p.Alerts {
			d.Alerts = append(d.Alerts, alertRow{Source: p.Name, Alert: a})
		}
	}

	// A tile shows only when its source is configured: one never set up is
	// left off the page, not shown as "Off".
	d.Tiles = []tile{servicesTile(d.Sections)}
	if snap.Federation != nil {
		d.Tiles = append(d.Tiles, federationTile(snap.Federation))
	}
	if snap.NetBird != nil {
		d.Tiles = append(d.Tiles, meshTile(snap.NetBird))
	}
	d.Tiles = append(d.Tiles, alertsTile(snap.Prometheus), fabricTile(snap.Portal))
	return d
}

// reservedIDs are element ids already on the page, so a link cannot take them.
var reservedIDs = map[string]bool{"observability": true, "offline-peers": true, "federation-peerings": true, "sp-live": true, "sp-frame": true}

// linkIDs gives each framed link a short, unique id made from its name, in
// config order. Links that open normally get "".
func linkIDs(sections []config.Section) []string {
	var ids []string
	used := map[string]bool{}
	for _, s := range sections {
		for _, l := range s.Links {
			if !l.Framed() {
				ids = append(ids, "")
				continue
			}
			base := slug(l.Name)
			id := base
			for n := 2; used[id] || reservedIDs[id] || strings.HasPrefix(id, "section-"); n++ {
				id = fmt.Sprintf("%s-%d", base, n)
			}
			used[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// slug keeps ASCII letters and digits, joined by single hyphens.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	if b.Len() == 0 {
		return "link"
	}
	return b.String()
}

func servicesTile(sections []sectionView) tile {
	up, checked, bad := 0, 0, 0
	pending := false
	for _, s := range sections {
		for _, l := range s.Links {
			switch l.Status.State {
			case collect.StateUp:
				up++
				checked++
			case collect.StateDown, collect.StateRefused:
				bad++
				checked++
			case collect.StatePending:
				pending = true
			}
		}
	}
	t := tile{Label: "Services"}
	switch {
	case pending:
		return tile{Label: "Services", Value: "—", Detail: "Checking", Chip: "muted", Status: "Pending"}
	case checked == 0:
		return tile{Label: "Services", Value: "—", Detail: "No checks configured", Chip: "muted", Status: "Off"}
	}
	t.Value = fmt.Sprintf("%d/%d", up, checked)
	t.Detail = "responding"
	if bad > 0 {
		t.Chip, t.Status = "err", fmt.Sprintf("%d down", bad)
	} else {
		t.Chip, t.Status = "ok", "All up"
	}
	return t
}

func meshTile(nb *collect.NetBirdStats) tile {
	t := tile{Label: "Mesh peers", Value: "—"}
	switch {
	case nb == nil || !nb.Configured:
		t.Detail, t.Chip, t.Status = "No token set", "muted", "Off"
	case nb.UpdatedAt.IsZero() && nb.Error == "":
		t.Detail, t.Chip, t.Status = "Checking", "muted", "Pending"
	case nb.UpdatedAt.IsZero():
		t.Detail, t.Chip, t.Status = nb.Error, "err", "Unavailable"
	default:
		t.Value = fmt.Sprintf("%d/%d", nb.Connected, nb.Total)
		t.Detail = "connected"
		switch {
		case nb.Error != "":
			t.Detail, t.Chip, t.Status = nb.Error, "warn", "Stale"
		case nb.Connected < nb.Total:
			t.Chip, t.Status = "warn", fmt.Sprintf("%d offline", nb.Total-nb.Connected)
		default:
			t.Chip, t.Status = "ok", "All online"
		}
	}
	return t
}

func federationTile(f *collect.FederationStats) tile {
	t := tile{Label: "Federation", Value: "—"}
	switch {
	case !f.Configured:
		t.Detail, t.Chip, t.Status = "No key set", "muted", "Off"
	case f.UpdatedAt.IsZero() && f.Error == "":
		t.Detail, t.Chip, t.Status = "Checking", "muted", "Pending"
	case f.UpdatedAt.IsZero():
		t.Detail, t.Chip, t.Status = f.Error, "err", "Unavailable"
	default:
		t.Value = fmt.Sprintf("%d/%d", f.Live, f.Total())
		t.Detail = "peerings live"
		switch {
		case f.Error != "":
			t.Detail, t.Chip, t.Status = f.Error, "warn", "Stale"
		case f.Total() == 0:
			t.Value, t.Detail, t.Chip, t.Status = "0", "No peerings yet", "muted", "None"
		case f.Attention > 0:
			t.Chip, t.Status = "err", fmt.Sprintf("%d need attention", f.Attention)
		case f.Setting > 0:
			t.Chip, t.Status = "info", fmt.Sprintf("%d in setup", f.Setting)
		default:
			t.Chip, t.Status = "ok", "All live"
		}
	}
	return t
}

// fedChip and fedLabel style a peering's state from Federation's summary.
func fedChip(state string) string {
	switch state {
	case "live":
		return "ok"
	case "setup":
		return "info"
	default:
		return "err"
	}
}

func fedLabel(state string) string {
	switch state {
	case "live":
		return "Live"
	case "setup":
		return "In setup"
	default:
		return "Attention"
	}
}

func alertsTile(proms []collect.PromStats) tile {
	t := tile{Label: "Firing alerts", Value: "—"}
	if len(proms) == 0 {
		t.Detail, t.Chip, t.Status = "No Prometheus set", "muted", "Off"
		return t
	}
	firing, reporting, failing := 0, 0, 0
	for _, p := range proms {
		if !p.UpdatedAt.IsZero() {
			reporting++
			firing += len(p.Alerts)
		}
		if p.Error != "" {
			failing++
		}
	}
	if reporting == 0 && failing == 0 {
		t.Detail, t.Chip, t.Status = "Checking", "muted", "Pending"
		return t
	}
	if reporting == 0 {
		t.Detail, t.Chip, t.Status = "No Prometheus reachable", "err", "Unavailable"
		return t
	}
	t.Value = fmt.Sprintf("%d", firing)
	t.Detail = fmt.Sprintf("across %d of %d Prometheus", reporting-staleCount(proms), len(proms))
	switch {
	case firing > 0:
		t.Chip, t.Status = "err", "Firing"
	case failing > 0:
		// Zero alerts from a partial view is not an all-clear.
		t.Chip, t.Status = "warn", "Partial"
	default:
		t.Chip, t.Status = "ok", "Clear"
	}
	return t
}

func staleCount(proms []collect.PromStats) int {
	n := 0
	for _, p := range proms {
		if p.Error != "" && !p.UpdatedAt.IsZero() {
			n++
		}
	}
	return n
}

func fabricTile(p *collect.PortalStats) tile {
	t := tile{Label: "Fabric orgs", Value: "—"}
	switch {
	case p == nil || !p.Configured:
		t.Detail, t.Chip, t.Status = "No key set", "muted", "Off"
	case p.UpdatedAt.IsZero() && p.Error == "":
		t.Detail, t.Chip, t.Status = "Checking", "muted", "Pending"
	case p.UpdatedAt.IsZero():
		t.Detail, t.Chip, t.Status = p.Error, "err", "Unavailable"
	default:
		if p.HasCatalog {
			t.Value = fmt.Sprintf("%d", p.Orgs)
		}
		switch {
		case p.HasCatalog && p.HasMachines:
			t.Detail = fmt.Sprintf("%s, %s", plural(p.Topics, "topic"), plural(p.Machines, "machine"))
		case p.HasCatalog:
			t.Detail = plural(p.Topics, "topic")
		default:
			t.Detail = plural(p.Machines, "machine")
		}
		switch {
		case p.Error != "":
			t.Detail, t.Chip, t.Status = p.Error, "warn", "Stale"
		case p.Simulated > 0:
			t.Chip, t.Status = "info", fmt.Sprintf("%d simulated", p.Simulated)
		default:
			t.Chip, t.Status = "ok", "Live"
		}
	}
	return t
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func ago(now, then time.Time) string {
	if then.IsZero() {
		return "never"
	}
	d := now.Sub(then)
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func chipClass(s collect.LinkState) string {
	switch s {
	case collect.StateUp:
		return "ok"
	case collect.StateDown:
		return "err"
	case collect.StateRefused:
		return "warn"
	}
	return "muted"
}

func stateLabel(s collect.LinkState) string {
	switch s {
	case collect.StateUp:
		return "Up"
	case collect.StateDown:
		return "Down"
	case collect.StateRefused:
		return "Refused"
	case collect.StatePending:
		return "Checking"
	}
	return "Not checked"
}

// meterClass marks usage that needs attention. The number is always shown too.
func meterClass(v float64) string {
	switch {
	case v >= 90:
		return "is-err"
	case v >= 75:
		return "is-warn"
	}
	return ""
}
