package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dev-tools/chekov/internal/nginx"
)

const (
	peerExportPath     = "/api/v1/exported-routes"
	peerPollPeriod     = 15 * time.Second
	peerGrace          = 45 * time.Second
	peerMaxBody        = 64 << 10
	noCompatibleExport = "No compatible export"
)

var errInvalidPeerExport = errors.New("invalid Chekov export")

type peerView struct {
	Host     string `json:"host"`
	Enabled  bool   `json:"enabled"`
	Status   string `json:"status"`
	Imported string `json:"imported"`
}

type peerExport struct {
	Kind      string   `json:"kind"`
	Hostnames []string `json:"hostnames"`
}

type peerResult struct {
	route   nginx.Route
	names   []string
	until   time.Time
	checked time.Time
	error   string
}

type peerAliases struct {
	mu      sync.Mutex
	enabled map[string]bool
	results map[string]peerResult
	path    string
	domain  string
	aliases *AliasManager
	config  *nginx.ConfigGenerator
}

func newPeerAliases(path, domain string, aliases *AliasManager, config *nginx.ConfigGenerator) (*peerAliases, error) {
	p := &peerAliases{
		path: path, domain: strings.ToLower(domain), aliases: aliases, config: config,
		enabled: make(map[string]bool), results: make(map[string]peerResult),
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		var names []string
		if err := json.Unmarshal(data, &names); err != nil {
			return nil, fmt.Errorf("read peer settings: %w", err)
		}
		for _, name := range names {
			p.enabled[name] = true
		}
	}
	return p, nil
}

func (p *peerAliases) set(host string, initial *peerResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.enabled)+1)
	for name := range p.enabled {
		if name != host {
			names = append(names, name)
		}
	}
	if initial != nil {
		names = append(names, host)
	}
	sort.Strings(names)
	data, err := json.Marshal(names)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".peer-aliases-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		return err
	}
	if initial != nil {
		p.enabled[host] = true
		p.results[host] = *initial
	} else {
		delete(p.enabled, host)
		delete(p.results, host)
	}
	return nil
}

func peerServiceRoute(route nginx.Route) bool {
	return route.ImportedFrom == "" && route.ContainerName != "" &&
		!strings.HasPrefix(route.ContainerName, "aws-tunnel-") &&
		route.TargetHost != "" && route.TargetPort > 0
}

func samePeerRoute(a, b nginx.Route) bool {
	return a.Hostname == b.Hostname && a.ContainerName == b.ContainerName && routeConnectivityKey(a) == routeConnectivityKey(b)
}

func (p *peerAliases) views(routes []nginx.Route) []peerView {
	imports := make(map[string][]string)
	for _, route := range routes {
		if route.ImportedFrom != "" {
			imports[route.ImportedFrom] = append(imports[route.ImportedFrom], route.Hostname)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	views := make([]peerView, 0)
	for _, route := range routes {
		if !peerServiceRoute(route) {
			continue
		}
		names := imports[route.Hostname]
		sort.Strings(names)
		view := peerView{Host: route.Hostname, Enabled: p.enabled[route.Hostname], Imported: strings.Join(names, ",")}
		result := p.results[route.Hostname]
		switch {
		case !view.Enabled:
		case !samePeerRoute(result.route, route):
			view.Status = "Waiting for first fetch"
		case result.error != "":
			view.Status = result.error
			if len(names) > 0 {
				view.Status += fmt.Sprintf(" (%d cached)", len(names))
			}
		case len(result.names) == 0:
			view.Status = "Connected — no aliases shared"
		default:
			count := len(names)
			view.Status = fmt.Sprintf("%d aliases imported", count)
			if conflicts := len(result.names) - count; conflicts > 0 {
				view.Status += fmt.Sprintf(", %d name conflicts", conflicts)
			}
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Host < views[j].Host })
	return views
}

func (p *peerAliases) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(p.views(p.config.CurrentRoutes())); err != nil {
			log.Printf("Write peer alias settings: %v", err)
		}
	case http.MethodPost:
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			http.Error(w, "Expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		var setting struct {
			Host    string `json:"host"`
			Enabled *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&setting); err != nil || setting.Enabled == nil {
			http.Error(w, "Expected a hostname and enabled flag", http.StatusBadRequest)
			return
		}
		route := findRouteByHostname(p.config.CurrentRoutes(), setting.Host)
		if route == nil || !peerServiceRoute(*route) {
			http.Error(w, "Not a mapped service route", http.StatusBadRequest)
			return
		}
		var initial *peerResult
		if *setting.Enabled {
			names, err := fetchPeerAliases(r.Context(), *route, p.domain)
			if err != nil {
				log.Printf("Check Chekov export for %q: %v", setting.Host, err)
				http.Error(w, noCompatibleExport, http.StatusConflict)
				return
			}
			current := findRouteByHostname(p.config.CurrentRoutes(), setting.Host)
			if current == nil || !samePeerRoute(*current, *route) {
				log.Printf("Check Chekov export for %q: backend changed during check", setting.Host)
				http.Error(w, noCompatibleExport, http.StatusConflict)
				return
			}
			now := time.Now()
			initial = &peerResult{route: *route, names: names, checked: now, until: now.Add(peerGrace)}
		}
		if err := p.set(setting.Host, initial); err != nil {
			log.Printf("Save peer alias setting: %v", err)
			http.Error(w, "Could not save setting", http.StatusInternalServerError)
			return
		}
		if err := p.refresh(); err != nil {
			log.Printf("Apply peer aliases for %q: %v", setting.Host, err)
			http.Error(w, "Setting saved, but Nginx could not be updated", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (p *peerAliases) export(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0)
	for _, route := range p.config.CurrentRoutes() {
		if peerServiceRoute(route) {
			names = append(names, route.Hostname)
		}
	}
	sort.Strings(names)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(peerExport{Kind: "chekov-route-export/v1", Hostnames: names}); err != nil {
		log.Printf("Write Chekov route export: %v", err)
	}
}

func fetchPeerAliases(ctx context.Context, route nginx.Route, domain string) ([]string, error) {
	key := routeConnectivityKey(route)
	// Use the discovered upstream's TLS policy and disable environment proxies.
	transport := newConnectivityTransport(key)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	address := url.URL{Scheme: key.Protocol, Host: net.JoinHostPort(key.Host, strconv.Itoa(key.Port)), Path: peerExportPath}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid peer backend: %w", err)
	}
	req.Host = "chekov." + domain
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request peer export: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("peer returned HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: HTTP %d", errInvalidPeerExport, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, peerMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read peer export: %w", err)
	}
	var export peerExport
	if len(data) > peerMaxBody {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", errInvalidPeerExport, peerMaxBody)
	}
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", errInvalidPeerExport, err)
	}
	if export.Kind != "chekov-route-export/v1" || export.Hostnames == nil {
		return nil, fmt.Errorf("%w: unsupported format", errInvalidPeerExport)
	}
	seen := make(map[string]bool)
	names := make([]string, 0, len(export.Hostnames))
	for _, raw := range export.Hostnames {
		name := strings.ToLower(raw)
		if name == "chekov."+domain || name != strings.TrimSpace(name) || len(name) > 253 ||
			validateHostnameUnderBaseDomain(name, domain) != nil {
			return nil, fmt.Errorf("%w: invalid hostname %q", errInvalidPeerExport, raw)
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, nil
}

func (p *peerAliases) run(ctx context.Context) {
	ticker := time.NewTicker(peerPollPeriod)
	defer ticker.Stop()
	for {
		p.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *peerAliases) poll(ctx context.Context) {
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for _, route := range p.config.CurrentRoutes() {
		p.mu.Lock()
		enabled := p.enabled[route.Hostname]
		p.mu.Unlock()
		if !enabled || !peerServiceRoute(route) || ctx.Err() != nil {
			continue
		}
		select {
		case limit <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-limit }()
			started := time.Now()
			names, err := fetchPeerAliases(ctx, route, p.domain)
			if err != nil && ctx.Err() == nil {
				log.Printf("Poll Chekov export for %q: %v", route.Hostname, err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			result := p.results[route.Hostname]
			if !p.enabled[route.Hostname] || result.checked.After(started) {
				return
			}
			if !samePeerRoute(result.route, route) {
				result = peerResult{route: route}
			}
			result.error = ""
			result.checked = time.Now()
			switch {
			case err == nil:
				result.names, result.until = names, result.checked.Add(peerGrace)
			case errors.Is(err, errInvalidPeerExport):
				result.error = noCompatibleExport + " — retrying"
				result.names, result.until = nil, time.Time{}
			default:
				result.error = "Peer unreachable — retrying"
			}
			p.results[route.Hostname] = result
		}()
	}
	wg.Wait()
	if ctx.Err() == nil {
		if err := p.refresh(); err != nil {
			log.Printf("Refresh peer aliases: %v", err)
		}
	}
}

func (p *peerAliases) refresh() error {
	changed, err := p.config.RefreshDecoratedRoutes()
	if err != nil {
		return err
	}
	if changed && (!p.config.ValidateConfig() || !p.config.ReloadNginx()) {
		return fmt.Errorf("could not load peer aliases into Nginx")
	}
	return nil
}

func (p *peerAliases) decorate(routes []nginx.Route) []nginx.Route {
	out := withoutImportedAliases(routes)
	occupied := map[string]bool{"chekov." + p.domain: true}
	for _, route := range out {
		occupied[strings.ToLower(route.Hostname)] = true
	}
	for _, item := range p.aliases.Snapshot() {
		occupied[item.AliasHostname] = true
	}
	claims := make(map[string][]nginx.Route)
	p.mu.Lock()
	now := time.Now()
	for _, route := range out {
		result := p.results[route.Hostname]
		if !p.enabled[route.Hostname] || !samePeerRoute(result.route, route) || !now.Before(result.until) {
			continue
		}
		for _, name := range result.names {
			if !occupied[name] {
				claims[name] = append(claims[name], route)
			}
		}
	}
	p.mu.Unlock()
	var names []string
	for name, owners := range claims {
		if len(owners) == 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var imported []nginx.Route
	for _, name := range names {
		route := claims[name][0]
		route.ImportedFrom = route.Hostname
		if route.Protocol == "https" && route.VerifyName == "" {
			route.VerifyName = route.Hostname
		}
		route.Hostname = name
		route.CertPath, route.KeyPath = "", ""
		route.AutoAliasSource = ""
		imported = append(imported, route)
	}
	if len(imported) > 0 {
		out = append(out, ensureRouteWildcardCerts(imported, p.domain)...)
	}
	return out
}

func withoutImportedAliases(routes []nginx.Route) []nginx.Route {
	out := make([]nginx.Route, 0, len(routes))
	for _, route := range routes {
		if route.ImportedFrom == "" {
			out = append(out, route)
		}
	}
	return out
}
