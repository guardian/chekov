package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-tools/chekov/internal/nginx"
)

func TestPeerExportSharesLocalServiceRoutesByDefault(t *testing.T) {
	dir := t.TempDir()
	config := nginx.NewConfigGenerator(filepath.Join(dir, "generated.conf"), "", "")
	routes := []nginx.Route{
		{Hostname: "api.local.dev", ContainerName: "api", TargetHost: "api", TargetPort: 8080},
		{Hostname: "api-alias.local.dev", ContainerName: "api", TargetHost: "api", TargetPort: 8080, ConfiguredAlias: true},
		{Hostname: "chekov.local.dev", TargetHost: "127.0.0.1", TargetPort: 18080},
		{Hostname: "inner-chekov.local.dev", TargetHost: "127.0.0.1", TargetPort: 18080, ConfiguredAlias: true},
		{Hostname: "tunnel.local.dev", ContainerName: "aws-tunnel-1", TargetHost: "127.0.0.1", TargetPort: 8888},
		{Hostname: "imported.local.dev", ImportedFrom: "peer.local.dev", ContainerName: "peer", TargetHost: "peer", TargetPort: 8080},
	}
	if err := config.ApplyRoutes(routes); err != nil {
		t.Fatal(err)
	}
	p, err := newPeerAliases(filepath.Join(dir, "peer-aliases.json"), "local.dev", NewAliasManager(), config)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.export(w, httptest.NewRequest(http.MethodGet, peerExportPath, nil))
	var got peerExport
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"api-alias.local.dev", "api.local.dev"}
	if w.Code != http.StatusOK || got.Kind != "chekov-route-export/v1" || !slices.Equal(got.Hostnames, want) {
		t.Fatalf("unexpected export: status %d, body %s", w.Code, w.Body)
	}
}

func TestPeerExportCapability(t *testing.T) {
	const domain = "local.dev"
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{"valid", 200, `{"kind":"chekov-route-export/v1","hostnames":["api.local.dev"]}`, []string{"api.local.dev"}},
		{"empty is supported", 200, `{"kind":"chekov-route-export/v1","hostnames":[]}`, []string{}},
		{"ordinary web page", 200, `<html></html>`, nil},
		{"missing endpoint", 404, "", nil},
		{"redirect", 302, "", nil},
		{"wrong protocol", 200, `{"kind":"other","hostnames":[]}`, nil},
		{"other domain", 200, `{"kind":"chekov-route-export/v1","hostnames":["api.example.org"]}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != peerExportPath || r.Host != "chekov."+domain {
					t.Errorf("unexpected request: %s %s %s", r.Method, r.Host, r.URL)
				}
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			got, problem := fetchPeerAliases(context.Background(), testConnectivityRoute(t, server.URL), domain)
			if (problem == nil) != (tt.want != nil) || !slices.Equal(got, tt.want) {
				t.Fatalf("got %v (%v), want %v", got, problem, tt.want)
			}
		})
	}
}

func TestPeerAliasAvailabilityAndPrecedence(t *testing.T) {
	const domain = "local.dev"
	path := filepath.Join(t.TempDir(), "peer-aliases.json")
	p, err := newPeerAliases(path, domain, NewAliasManager(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first := nginx.Route{Hostname: "first.local.dev", ContainerName: "first", TargetHost: "first", TargetPort: 8080}
	second := nginx.Route{Hostname: "second.local.dev", ContainerName: "second", TargetHost: "second", TargetPort: 8080}
	routes := []nginx.Route{first, second}
	page := renderAliasPage("", nil, nil, nil, p.views(routes)...)
	if !strings.Contains(page, `data-fetch-aliases="first.local.dev">`) {
		t.Fatal("unchecked routes must allow an initial check")
	}

	const imported = "api.local.dev"
	claim := func(route nginx.Route) {
		if err := p.set(route.Hostname, &peerResult{route: route, names: []string{imported}, until: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	claim(first)
	got := p.decorate(routes)
	alias := findRouteByHostname(got, imported)
	if alias == nil || alias.ImportedFrom != first.Hostname || alias.TargetHost != first.TargetHost || alias.TargetPort != first.TargetPort {
		t.Fatalf("expected alias through selected peer: %+v", got)
	}
	if view := p.views(got)[0]; !view.Enabled || view.Imported != imported {
		t.Fatalf("expected compatible peer and imported hostname: %+v", view)
	}
	restored, err := newPeerAliases(path, domain, NewAliasManager(), nil)
	if err != nil || !restored.enabled[first.Hostname] {
		t.Fatalf("checkbox setting was not restored: %v", err)
	}
	local := nginx.Route{Hostname: imported, TargetHost: "local", TargetPort: 9000}
	if got := p.decorate(append(slices.Clone(got), local)); len(got) != 3 || got[2] != local {
		t.Fatalf("import should yield to local route: %+v", got)
	}
	claim(second)
	if got := p.decorate(got); len(got) != 2 {
		t.Fatalf("conflicting peer imports should both be skipped: %+v", got)
	}
	if err := p.set(first.Hostname, nil); err != nil {
		t.Fatal(err)
	}

	got = p.decorate(routes)
	if alias := findRouteByHostname(got, imported); alias == nil || alias.ImportedFrom != second.Hostname {
		t.Fatalf("remaining peer should win when the other is unchecked: %+v", got)
	}
	p.results[second.Hostname] = peerResult{route: second, names: []string{imported}, until: time.Now().Add(-time.Second)}
	if got := p.decorate(got); len(got) != 2 {
		t.Fatalf("expired peer imports were retained: %+v", got)
	}
}

func TestPeerSettingsChecksOnlyOnOptIn(t *testing.T) {
	var calls atomic.Int32
	var compatible atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !compatible.Load() {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"kind":"chekov-route-export/v1","hostnames":[]}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "peer-aliases.json")
	config := nginx.NewConfigGenerator(filepath.Join(dir, "generated.conf"), "", "")
	route := testConnectivityRoute(t, server.URL)
	route.ContainerName = "peer"
	if err := config.ApplyRoutes([]nginx.Route{route}); err != nil {
		t.Fatal(err)
	}
	p, err := newPeerAliases(path, "local.dev", NewAliasManager(), config)
	if err != nil {
		t.Fatal(err)
	}
	toggleOn := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/peer-aliases", strings.NewReader(fmt.Sprintf(`{"host":%q,"enabled":true}`, route.Hostname)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		p.settings(w, r)
		return w
	}
	p.poll(context.Background())
	if calls.Load() != 0 {
		t.Fatal("unchecked route was probed")
	}
	if w := toggleOn(); w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != "No compatible export" {
		t.Fatalf("unexpected failure: %d %q", w.Code, w.Body.String())
	}
	p.poll(context.Background())
	if _, err := os.Stat(path); !os.IsNotExist(err) || calls.Load() != 1 || p.views([]nginx.Route{route})[0].Enabled {
		t.Fatalf("failed check must not save or start polling: stat=%v, calls=%d", err, calls.Load())
	}
	compatible.Store(true)
	if w := toggleOn(); w.Code != http.StatusNoContent {
		t.Fatalf("retry with valid empty export failed: %d %q", w.Code, w.Body.String())
	}
	p.poll(context.Background())
	if view := p.views([]nginx.Route{route})[0]; !view.Enabled || view.Status != "Connected — no aliases shared" || calls.Load() != 3 {
		t.Fatalf("valid empty export must enable polling: %+v, calls=%d", view, calls.Load())
	}
}
