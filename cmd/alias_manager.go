package main

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/dev-tools/chekov/internal/nginx"
)

var aliasHostnameLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type AliasEntry struct {
	Automatic      bool
	Imported       bool
	Configured     bool
	SourceHostname string
	AliasHostname  string
	TargetHost     string
	TargetPort     int
	Protocol       string
	VerifySSL      bool
	VerifyName     string
	CertPath       string
	KeyPath        string
}

type AliasManager struct {
	mu         sync.RWMutex
	items      []AliasEntry
	configured []AliasEntry // Loaded before the manager is shared.
}

func NewAliasManager() *AliasManager {
	return &AliasManager{}
}

func parseConfiguredAliases(raw, baseDomain string) ([]AliasEntry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))
	if baseDomain == "" {
		return nil, fmt.Errorf("base domain is required")
	}
	var entries []AliasEntry
	seen := make(map[string]bool)
	for _, pair := range strings.Split(raw, ",") {
		alias, source, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("expected alias=route, got %q", pair)
		}
		aliasHostname, err := normalizeAliasHostname(alias, baseDomain)
		if err != nil {
			return nil, fmt.Errorf("invalid alias in %q: %w", pair, err)
		}
		sourceHostname, err := normalizeAliasHostname(source, baseDomain)
		if err != nil {
			return nil, fmt.Errorf("invalid route in %q: %w", pair, err)
		}
		if aliasHostname == sourceHostname {
			return nil, fmt.Errorf("alias %q must differ from its route", aliasHostname)
		}
		if seen[aliasHostname] {
			return nil, fmt.Errorf("alias %q is configured more than once", aliasHostname)
		}
		seen[aliasHostname] = true
		entries = append(entries, AliasEntry{Configured: true, AliasHostname: aliasHostname, SourceHostname: sourceHostname})
	}
	return entries, nil
}

func (m *AliasManager) Snapshot() []AliasEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AliasEntry, len(m.items))
	copy(out, m.items)
	return out
}

func (m *AliasManager) AddAlias(sourceHostname string, rawAlias string, baseDomain string, routes []nginx.Route) (AliasEntry, error) {
	sourceHostname = strings.TrimSpace(sourceHostname)
	rawAlias = strings.TrimSpace(rawAlias)
	baseDomain = strings.TrimSpace(strings.ToLower(baseDomain))
	if sourceHostname == "" {
		return AliasEntry{}, fmt.Errorf("choose a mapped domain first")
	}
	if rawAlias == "" {
		return AliasEntry{}, fmt.Errorf("alias is required")
	}
	if baseDomain == "" {
		return AliasEntry{}, fmt.Errorf("base domain is required")
	}

	aliasHostname, err := normalizeAliasHostname(rawAlias, baseDomain)
	if err != nil {
		return AliasEntry{}, err
	}
	match := findRouteByHostname(routes, sourceHostname)
	if match == nil {
		sourceHostname, err = normalizeAliasHostname(sourceHostname, baseDomain)
		if err != nil {
			return AliasEntry{}, fmt.Errorf("invalid source hostname: %w", err)
		}
		match = findRouteByHostname(routes, sourceHostname)
	}
	if match == nil {
		return AliasEntry{}, fmt.Errorf("hostname %q is not currently mapped", sourceHostname)
	}
	if aliasHostname == sourceHostname {
		return AliasEntry{}, fmt.Errorf("alias must be different from the mapped hostname")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.configured {
		if item.AliasHostname == aliasHostname {
			return AliasEntry{}, fmt.Errorf("alias %q is reserved by CHEKOV_ALIASES", aliasHostname)
		}
	}
	for _, item := range m.items {
		if item.SourceHostname == sourceHostname && item.AliasHostname == aliasHostname {
			return item, nil
		}
		if item.AliasHostname == aliasHostname {
			return AliasEntry{}, fmt.Errorf("alias %q already exists", aliasHostname)
		}
	}
	if findRouteByHostname(routes, aliasHostname) != nil {
		return AliasEntry{}, fmt.Errorf("hostname %q is already mapped", aliasHostname)
	}

	entry := AliasEntry{
		SourceHostname: sourceHostname,
		AliasHostname:  aliasHostname,
		TargetHost:     match.TargetHost,
		TargetPort:     match.TargetPort,
		Protocol:       match.Protocol,
		VerifySSL:      match.VerifySSL,
		VerifyName:     match.VerifyName,
		CertPath:       match.CertPath,
		KeyPath:        match.KeyPath,
	}
	m.items = append(m.items, entry)
	return entry, nil
}

func (m *AliasManager) MergeRoutes(routes []nginx.Route) []nginx.Route {
	return mergeManualAliases(routes, m.Snapshot())
}

func mergeManualAliases(routes []nginx.Route, items []AliasEntry) []nginx.Route {
	out := append([]nginx.Route(nil), routes...)
	for _, item := range items {
		source := findRouteByHostname(routes, item.SourceHostname)
		if source == nil {
			continue
		}
		if findRouteByHostname(out, item.AliasHostname) != nil {
			continue
		}
		aliasRoute := *source
		aliasRoute.Hostname = item.AliasHostname
		aliasRoute.ConfiguredAlias = false
		aliasRoute.CertPath = item.CertPath
		aliasRoute.KeyPath = item.KeyPath
		if aliasRoute.CertPath == "" {
			aliasRoute.CertPath = source.CertPath
		}
		if aliasRoute.KeyPath == "" {
			aliasRoute.KeyPath = source.KeyPath
		}
		out = append(out, aliasRoute)
	}
	return out
}

func withoutAutomaticAliases(routes []nginx.Route) []nginx.Route {
	out := make([]nginx.Route, 0, len(routes))
	for _, route := range routes {
		if route.AutoAliasSource == "" {
			out = append(out, route)
		}
	}
	return out
}

func (m *AliasManager) mergeAllAliases(routes []nginx.Route, rows []ContainerDiagnostic, baseDomain string) []nginx.Route {
	base := withoutAutomaticAliases(withoutImportedAliases(routes))
	items := m.Snapshot()
	out := mergeManualAliases(base, items)
	manual := make(map[string]bool)
	occupied := make(map[string]bool, len(out))
	for _, entry := range items {
		manual[entry.AliasHostname] = true
		occupied[entry.AliasHostname] = true
	}
	for _, route := range out {
		occupied[strings.ToLower(route.Hostname)] = true
	}
	for _, entry := range m.configured {
		occupied[entry.AliasHostname] = true
	}

	byContainer := make(map[string][]nginx.Route)
	primaryCount := make(map[string]int, len(base))
	for _, route := range base {
		if route.ContainerName != "" && !route.ConfiguredAlias && !manual[route.Hostname] {
			byContainer[route.ContainerName] = append(byContainer[route.ContainerName], route)
		}
		primaryCount[strings.ToLower(route.Hostname)]++
	}
	claims := make(map[string][]nginx.Route)
	for _, row := range rows {
		targets := byContainer[row.Name]
		if len(targets) != 1 || len(row.DevContainerNames) != 1 ||
			primaryCount[strings.ToLower(targets[0].Hostname)] != 1 {
			continue
		}
		name, err := normalizeAliasHostname(row.DevContainerNames[0], strings.ToLower(baseDomain))
		if err == nil && !occupied[name] {
			claims[name] = append(claims[name], targets[0])
		}
	}
	names := make([]string, 0, len(claims))
	for name, targets := range claims {
		if len(targets) == 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		route := claims[name][0]
		route.AutoAliasSource = route.Hostname
		route.Hostname = name
		out = append(out, route)
	}
	return out
}

func (m *AliasManager) listedAliases(routes []nginx.Route) []AliasEntry {
	items := append([]AliasEntry(nil), m.configured...)
	items = append(items, m.Snapshot()...)
	for _, route := range routes {
		if route.ImportedFrom != "" {
			items = append(items, AliasEntry{Imported: true, SourceHostname: route.ImportedFrom, AliasHostname: route.Hostname})
		}
		if route.AutoAliasSource != "" {
			items = append(items, AliasEntry{
				Automatic:      true,
				SourceHostname: route.AutoAliasSource,
				AliasHostname:  route.Hostname,
			})
		}
	}
	return items
}

func (m *AliasManager) mergeConfiguredAliases(routes []nginx.Route) []nginx.Route {
	if len(m.configured) == 0 {
		return routes
	}
	out := make([]nginx.Route, 0, len(routes)+len(m.configured))
	byHostname := make(map[string][]nginx.Route)
	for _, route := range routes {
		if !route.ConfiguredAlias {
			out = append(out, route)
			name := strings.ToLower(route.Hostname)
			byHostname[name] = append(byHostname[name], route)
		}
	}
	for _, item := range m.configured {
		sources := byHostname[item.SourceHostname]
		switch {
		case len(byHostname[item.AliasHostname]) != 0:
			log.Printf("WARNING: CHEKOV_ALIASES: alias %q conflicts with existing route", item.AliasHostname)
		case len(sources) == 0 || (len(sources) == 1 && sources[0].AutoAliasSource != ""):
			log.Printf("WARNING: CHEKOV_ALIASES: alias %q is waiting for route %q", item.AliasHostname, item.SourceHostname)
		case len(sources) > 1:
			log.Printf("WARNING: CHEKOV_ALIASES: alias %q has ambiguous route %q", item.AliasHostname, item.SourceHostname)
		default:
			alias := sources[0]
			alias.Hostname = item.AliasHostname
			alias.ConfiguredAlias = true
			out = append(out, alias)
		}
	}
	return out
}

func (m *AliasManager) RemoveAlias(aliasHostname string) bool {
	aliasHostname = strings.TrimSpace(strings.ToLower(aliasHostname))
	if aliasHostname == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.items[:0]
	changed := false
	for _, item := range m.items {
		if strings.EqualFold(item.AliasHostname, aliasHostname) {
			changed = true
			continue
		}
		filtered = append(filtered, item)
	}
	m.items = filtered
	return changed
}

func normalizeAliasHostname(rawAlias string, baseDomain string) (string, error) {
	alias := strings.TrimSpace(strings.ToLower(rawAlias))
	alias = strings.TrimPrefix(alias, "https://")
	alias = strings.TrimPrefix(alias, "http://")
	alias = strings.Trim(alias, ".")
	if alias == "" {
		return "", fmt.Errorf("alias is required")
	}
	if strings.HasSuffix(alias, "."+baseDomain) {
		full := alias
		if err := validateHostnameUnderBaseDomain(full, baseDomain); err != nil {
			return "", err
		}
		return full, nil
	}
	full := alias + "." + baseDomain
	if err := validateHostnameUnderBaseDomain(full, baseDomain); err != nil {
		return "", fmt.Errorf("alias must be within the %s namespace: %w", baseDomain, err)
	}
	return full, nil
}

func validateHostnameUnderBaseDomain(hostname string, baseDomain string) error {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	baseDomain = strings.TrimSpace(strings.ToLower(baseDomain))
	if hostname == "" || baseDomain == "" {
		return fmt.Errorf("host is empty")
	}
	if hostname == baseDomain {
		return fmt.Errorf("host must not equal the base domain")
	}
	if !strings.HasSuffix(hostname, "."+baseDomain) {
		return fmt.Errorf("host %q is outside the %q namespace", hostname, baseDomain)
	}
	labelText := strings.TrimSuffix(hostname, "."+baseDomain)
	if labelText == "" {
		return fmt.Errorf("host must include a label before %q", baseDomain)
	}
	labels := strings.Split(labelText, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("host contains an empty label")
		}
		if len(label) > 63 || !aliasHostnameLabelRE.MatchString(label) {
			return fmt.Errorf("host label %q is not valid", label)
		}
	}
	return nil
}

func findRouteByHostname(routes []nginx.Route, hostname string) *nginx.Route {
	hostname = strings.TrimSpace(hostname)
	for i := range routes {
		if routes[i].Hostname == hostname {
			return &routes[i]
		}
	}
	return nil
}

func routeHostnames(routes []nginx.Route) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.Hostname == "" || seen[route.Hostname] {
			continue
		}
		seen[route.Hostname] = true
		out = append(out, route.Hostname)
	}
	sort.Strings(out)
	return out
}

func aliasSuggestionsForRoutes(routes []nginx.Route, rows []ContainerDiagnostic) map[string][]string {
	byContainer := make(map[string][]string, len(rows))
	for _, row := range rows {
		byContainer[row.Name] = row.AliasSuggestions
	}
	suggestions := make(map[string][]string)
	for _, route := range routes {
		if names := byContainer[route.ContainerName]; len(names) > 0 {
			suggestions[route.Hostname] = names
		}
	}
	return suggestions
}

func filterAliasHostnameRoutes(routes []nginx.Route, aliases ...string) []nginx.Route {
	if len(routes) == 0 || len(aliases) == 0 {
		return routes
	}
	blocked := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(strings.ToLower(alias))
		if alias != "" {
			blocked[alias] = true
		}
	}
	out := make([]nginx.Route, 0, len(routes))
	for _, route := range routes {
		if blocked[strings.TrimSpace(strings.ToLower(route.Hostname))] {
			continue
		}
		out = append(out, route)
	}
	return out
}
