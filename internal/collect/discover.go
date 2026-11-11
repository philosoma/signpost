package collect

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/philosoma/signpost/internal/config"
)

// Route files are small; a larger one is not a route file.
const maxRouteFile = 1 << 20

var hostRule = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// routedHosts returns every Host(`...`) name in the route files in dir, sorted.
// Only the names are kept; nothing else in the files is read or stored.
func routedHosts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yml" && ext != ".yaml" && ext != ".toml") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxRouteFile {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, m := range hostRule.FindAllSubmatch(b, -1) {
			h := strings.ToLower(string(m[1]))
			// Skip templated or malformed names.
			if strings.ContainsAny(h, "<>{}$ /") {
				continue
			}
			seen[h] = true
		}
	}
	hosts := make([]string, 0, len(seen))
	for h := range seen {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts, nil
}

// layout merges the discovered apps with the configured links into sections.
//
//   - Catalogue apps whose route exists, in catalogue order.
//   - Configured links, after the apps in the same section.
//   - Routed apps the catalogue does not name, in the other section, last.
//
// Sections follow SectionOrder, then the order they first appear.
func layout(cfg *config.Config, hosts []string) []config.Section {
	d := cfg.Discover
	if d == nil {
		return cfg.Sections
	}
	suffix := "." + strings.ToLower(strings.Trim(d.Domain, "."))
	routed := map[string]bool{}
	for _, h := range hosts {
		if p, ok := strings.CutSuffix(h, suffix); ok && p != "" {
			routed[p] = true
		}
	}
	hidden := func(prefix string) bool { return slices.Contains(d.Hide, prefix) }

	bySection := map[string][]config.Link{}
	var titles []string
	add := func(title string, l config.Link) {
		if _, ok := bySection[title]; !ok {
			titles = append(titles, title)
		}
		bySection[title] = append(bySection[title], l)
	}

	named := map[string]bool{}
	for _, e := range d.Catalogue {
		named[e.Prefix] = true
		if !routed[e.Prefix] || hidden(e.Prefix) {
			continue
		}
		root := "https://" + e.Prefix + suffix + "/"
		l := config.Link{
			Name:        e.Name,
			URL:         root,
			Description: e.Description,
			Probe:       e.Probe == nil || *e.Probe,
			Frame:       e.Frame,
		}
		if e.Path != "" && e.Path != "/" {
			l.URL = "https://" + e.Prefix + suffix + e.Path
			l.ProbeURL = root
		}
		add(e.Section, l)
	}
	for _, s := range cfg.Sections {
		for _, l := range s.Links {
			add(s.Title, l)
		}
	}
	var others []string
	for p := range routed {
		if !named[p] && !hidden(p) {
			others = append(others, p)
		}
	}
	sort.Strings(others)
	for _, p := range others {
		add(d.OtherSection, config.Link{
			Name:        titleCase(p),
			URL:         "https://" + p + suffix + "/",
			Description: "Found on this node. Name it in the catalogue to move it.",
			Probe:       true,
		})
	}

	order := append([]string(nil), d.SectionOrder...)
	for _, t := range titles {
		if t != d.OtherSection && !slices.Contains(order, t) {
			order = append(order, t)
		}
	}
	if !slices.Contains(order, d.OtherSection) {
		order = append(order, d.OtherSection)
	}
	var out []config.Section
	for _, t := range order {
		if links := bySection[t]; len(links) > 0 {
			out = append(out, config.Section{Title: t, Links: links})
		}
	}
	return out
}

// titleCase turns "air-defenders" or "admin.auth" into "Air Defenders" or "Admin Auth".
func titleCase(prefix string) string {
	words := strings.FieldsFunc(prefix, func(r rune) bool { return r == '-' || r == '.' || r == '_' })
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// coreDialer sends connections for hosts under domain to the core address,
// keeping the name for TLS. Other hosts resolve as usual.
func coreDialer(domain, address string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	suffix := "." + strings.ToLower(strings.Trim(domain, "."))
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err == nil && strings.HasSuffix(strings.ToLower(host), suffix) {
			addr = net.JoinHostPort(address, port)
		}
		return d.DialContext(ctx, network, addr)
	}
}
