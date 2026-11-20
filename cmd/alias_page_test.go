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
	current := addHomepageAndAliasRoutes(nil, baseDomain, manager, nil)
	if _, err := manager.AddAlias("chekov", "inner-chekov", baseDomain, current); err != nil {
		t.Fatal(err)
	}

	service := nginx.Route{Hostname: "api." + baseDomain, TargetHost: "api", TargetPort: 8080}
	rebuilt := addHomepageAndAliasRoutes([]nginx.Route{service}, baseDomain, manager, nil)
	want := []nginx.Route{
		service,
		{Hostname: "chekov." + baseDomain, TargetHost: "127.0.0.1", TargetPort: 18080},
		{Hostname: "inner-chekov." + baseDomain, TargetHost: "127.0.0.1", TargetPort: 18080},
	}
	if !reflect.DeepEqual(rebuilt, want) {
		t.Fatalf("rebuilt routes = %+v, want %+v", rebuilt, want)
	}
}

func TestAutomaticDevContainerAlias(t *testing.T) {
	const base = "local.dev"
	const alias = "inner-chekov." + base
	container := &discovery.ContainerInfo{
		ID:       "container-1",
		Name:     "zen_tu",
		Networks: []string{"bridge"},
		Labels: map[string]string{
			"com.intellij.devcontainer.initial.model": `{"name":"inner-chekov","forwardPorts":[8080]}`,
			"com.intellij.devcontainer.sources.path":  "/code/workspace-folder",
		},
	}
	routes, row := diagnoseContainer(container, discovery.NewIdentifier(), discovery.NewNetworkResolver("proxy"), base)
	if len(routes) != 1 || routes[0].Hostname != "zen-tu."+base {
		t.Fatalf("unexpected primary routes: %+v", routes)
	}
	manager := NewAliasManager()
	rows := []ContainerDiagnostic{row}
	merged := addHomepageAndAliasRoutes(routes, base, manager, rows)
	want := routes[0]
	want.AutoAliasSource = want.Hostname
	want.Hostname = alias
	if got := findRouteByHostname(merged, alias); got == nil || *got != want || len(merged) != 3 {
		t.Fatalf("expected unchanged primary, homepage and metadata alias: %+v", merged)
	}
	if got := manager.mergeAllAliases(merged, nil, base); findRouteByHostname(got, alias) != nil {
		t.Fatalf("stale automatic alias was retained: %+v", got)
	}
	page := renderAliasPage("", nil, manager.listedAliases(merged), nil)
	if !strings.Contains(page, "<td>"+alias+"</td><td>Automatic</td>") || strings.Contains(page, "data-delete-alias") {
		t.Fatal("automatic alias should be listed without a delete action")
	}

	if _, err := manager.AddAlias("chekov", "inner-chekov", base, withoutAutomaticAliases(merged)); err != nil {
		t.Fatal(err)
	}
	overridden := manager.mergeAllAliases(merged, rows, base)
	if got := findRouteByHostname(overridden, alias); got == nil || got.AutoAliasSource != "" || got.TargetPort != 18080 {
		t.Fatalf("manual alias did not take precedence: %+v", overridden)
	}
	manager.RemoveAlias(alias)
	restored := manager.mergeAllAliases(filterAliasHostnameRoutes(overridden, alias), rows, base)
	if got := findRouteByHostname(restored, alias); got == nil || *got != want {
		t.Fatalf("automatic alias was not restored: %+v", restored)
	}
}

func TestAutomaticAliasSkipsCollisionsAndAmbiguousTargets(t *testing.T) {
	const base = "local.dev"
	source := nginx.Route{Hostname: "primary." + base, ContainerName: "first", TargetPort: 8080}
	other := nginx.Route{Hostname: "other." + base, ContainerName: "second", TargetPort: 8080}
	first := ContainerDiagnostic{Name: "first", DevContainerNames: []string{"friendly"}}
	for _, tt := range []struct {
		name   string
		routes []nginx.Route
		rows   []ContainerDiagnostic
	}{
		{"existing route", []nginx.Route{source, {Hostname: "friendly." + base}}, []ContainerDiagnostic{first}},
		{"ambiguous primary hostname", []nginx.Route{source, {Hostname: source.Hostname}}, []ContainerDiagnostic{first}},
		{"built-in route", []nginx.Route{source}, []ContainerDiagnostic{{Name: "first", DevContainerNames: []string{"chekov"}}}},
		{"duplicate metadata name", []nginx.Route{source, other}, []ContainerDiagnostic{first, {Name: "second", DevContainerNames: []string{"friendly"}}}},
		{"multiple endpoints", []nginx.Route{source, {Hostname: "second-port." + base, ContainerName: "first", TargetPort: 9090}}, []ContainerDiagnostic{first}},
		{"multiple metadata names", []nginx.Route{source}, []ContainerDiagnostic{{Name: "first", DevContainerNames: []string{"friendly", "other"}}}},
		{"folder suggestion only", []nginx.Route{source}, []ContainerDiagnostic{{Name: "first", AliasSuggestions: []string{"friendly"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := addHomepageAndAliasRoutes(tt.routes, base, NewAliasManager(), tt.rows)
			if len(got) != len(tt.routes)+1 {
				t.Fatalf("unexpected automatic alias in %+v", got)
			}
		})
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
