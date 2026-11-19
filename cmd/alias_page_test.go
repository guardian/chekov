package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dev-tools/chekov/internal/discovery"
	"github.com/dev-tools/chekov/internal/nginx"
)

func TestHomepageAliasSurvivesRouteRebuild(t *testing.T) {
	const baseDomain = "local.dev"
	manager := NewAliasManager()
	current := addHomepageAndAliasRoutes(nil, baseDomain, manager)
	if _, err := manager.AddAlias("chekov", "inner-chekov", baseDomain, current); err != nil {
		t.Fatal(err)
	}

	service := nginx.Route{Hostname: "api." + baseDomain, TargetHost: "api", TargetPort: 8080}
	rebuilt := addHomepageAndAliasRoutes([]nginx.Route{service}, baseDomain, manager)
	want := []nginx.Route{
		service,
		{Hostname: "chekov." + baseDomain, TargetHost: "127.0.0.1", TargetPort: 18080},
		{Hostname: "inner-chekov." + baseDomain, TargetHost: "127.0.0.1", TargetPort: 18080},
	}
	if !reflect.DeepEqual(rebuilt, want) {
		t.Fatalf("rebuilt routes = %+v, want %+v", rebuilt, want)
	}
}

func TestAliasSourceAcceptsShortHostnames(t *testing.T) {
	for _, baseDomain := range []string{"local.dev-gutools.co.uk", "custom.local.dev"} {
		t.Run(baseDomain, func(t *testing.T) {
			source := "api." + baseDomain
			routes := []nginx.Route{
				{Hostname: source, TargetHost: "127.0.0.1", TargetPort: 8080},
				{Hostname: "prod.api." + baseDomain, TargetHost: "127.0.0.1", TargetPort: 9090},
			}
			manager := NewAliasManager()
			short, err := manager.AddAlias("api", "public", baseDomain, routes)
			if err != nil {
				t.Fatal(err)
			}
			full, err := manager.AddAlias(source, "public", baseDomain, routes)
			if err != nil || !reflect.DeepEqual(short, full) || len(manager.Snapshot()) != 1 {
				t.Fatalf("short and full source should be equivalent: short=%+v full=%+v err=%v", short, full, err)
			}
			if short.SourceHostname != source || short.TargetPort != 8080 {
				t.Fatalf("unexpected normalized source or target: %+v", short)
			}
			dotted, err := manager.AddAlias("prod.api", "production", baseDomain, routes)
			if err != nil || dotted.SourceHostname != "prod.api."+baseDomain || dotted.TargetPort != 9090 {
				t.Fatalf("dotted source failed: %+v, %v", dotted, err)
			}
			if _, err := manager.AddAlias("api", "api", baseDomain, routes); err == nil {
				t.Fatal("self-alias should be rejected after source normalization")
			}
			for _, invalid := range []string{"", "missing", "bad/name"} {
				if _, err := manager.AddAlias(invalid, "other", baseDomain, routes); err == nil {
					t.Errorf("unmapped or invalid source %q was accepted", invalid)
				}
			}
			merged := manager.MergeRoutes(routes)
			if len(merged) != 4 {
				t.Fatalf("normalized source aliases not merged: %+v", merged)
			}
		})
	}
}

func TestAliasPageExcludesChekovMappedDomain(t *testing.T) {
	for _, baseDomain := range []string{"local.dev-gutools.co.uk", "custom.local.dev"} {
		t.Run(baseDomain, func(t *testing.T) {
			chekovHostname := "chekov." + baseDomain
			serviceHostname := "xxx." + baseDomain
			routes := []nginx.Route{{Hostname: chekovHostname}, {Hostname: serviceHostname}}
			filtered := filterAliasHostnameRoutes(routes, chekovHostname)
			page := renderAliasPage("", routeHostnames(filtered), nil, nil)
			if strings.Contains(page, `<option value="`+chekovHostname+`"`) {
				t.Fatal("Chekov appears in mapped domains")
			}
			if !strings.Contains(page, `<option value="`+serviceHostname+`"`) {
				t.Fatal("service missing from mapped domains")
			}
			if len(routes) != 2 {
				t.Fatal("filter changed original routes")
			}
			page = renderAliasPage("", routeHostnames(filterAliasHostnameRoutes(routes[:1], chekovHostname)), nil, nil)
			if !strings.Contains(page, `<option value="">No mapped domains</option>`) {
				t.Fatal("Chekov-only routes should show no mapped domains")
			}
		})
	}
}

func TestAliasPageSuggestionsFollowSourceContainer(t *testing.T) {
	routes := []nginx.Route{
		{Hostname: "vigorous-carver.local.dev", ContainerName: "vigorous_carver"},
		{Hostname: "other.local.dev", ContainerName: "other"},
		{Hostname: "tunnel.local.dev", ContainerName: "aws-tunnel-1"},
	}
	rows := []ContainerDiagnostic{
		{Name: "vigorous_carver", AliasSuggestions: []string{"dotcom-rendering"}},
		{Name: "other", AliasSuggestions: []string{"amiable"}},
	}
	suggestions := aliasSuggestionsForRoutes(routes, rows)
	if len(suggestions) != 2 || len(suggestions[routes[0].Hostname]) != 1 ||
		suggestions[routes[0].Hostname][0] != "dotcom-rendering" {
		t.Fatalf("unexpected route suggestions: %#v", suggestions)
	}
	page := renderAliasPage("", []string{routes[0].Hostname, routes[1].Hostname, routes[2].Hostname}, nil, suggestions)
	for _, expected := range []string{
		`name="alias" id="alias-value" list="alias-suggestions-0"`,
		`data-suggestions="alias-suggestions-1"`,
		`<datalist id="alias-suggestions-0"><option value="dotcom-rendering"></option></datalist>`,
		`<datalist id="alias-suggestions-1"><option value="amiable"></option></datalist>`,
		`<datalist id="alias-suggestions-2"></datalist>`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("page missing %s", expected)
		}
	}
}

func TestAliasPageEscapesSuggestions(t *testing.T) {
	page := renderAliasPage("", []string{"service.local.dev"}, nil, map[string][]string{
		"service.local.dev": {`"><script>alert(1)</script>`},
	})
	if strings.Contains(page, `<script>alert(1)</script>`) ||
		!strings.Contains(page, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatal("suggestion was not escaped")
	}
}
func TestAliasPageUsesDiscoveredProjectSuggestion(t *testing.T) {
	container := &discovery.ContainerInfo{
		ID:       "container-1",
		Name:     "vigorous_carver",
		Networks: []string{"bridge"},
		Labels: map[string]string{
			"devcontainer.local_folder": "/Users/developer/code/dotcom-rendering",
			"devcontainer.metadata":     `[{"forwardPorts":[3030]}]`,
		},
	}
	routes, diagnostic := diagnoseContainer(container, discovery.NewIdentifier(), discovery.NewNetworkResolver("proxy"), "local.dev")
	if len(routes) != 1 || routes[0].Hostname != "vigorous-carver.local.dev" {
		t.Fatalf("unexpected discovered routes: %#v", routes)
	}
	suggestions := aliasSuggestionsForRoutes(routes, []ContainerDiagnostic{diagnostic})
	page := renderAliasPage("", routeHostnames(routes), nil, suggestions)
	if !strings.Contains(page, `<option value="dotcom-rendering"></option>`) {
		t.Fatal("discovered project suggestion missing from alias page")
	}
	manager := NewAliasManager()
	for _, name := range []string{"dotcom-rendering", "my-preference"} {
		entry, err := manager.AddAlias(routes[0].Hostname, name, "local.dev", routes)
		if err != nil || entry.AliasHostname != name+".local.dev" {
			t.Fatalf("alias %q failed: %#v, %v", name, entry, err)
		}
	}
}
