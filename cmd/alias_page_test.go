package main

import (
	"strings"
	"testing"

	"github.com/dev-tools/chekov/internal/discovery"
	"github.com/dev-tools/chekov/internal/nginx"
)

func TestAliasPageExcludesChekovMappedDomain(t *testing.T) {
	for _, baseDomain := range []string{"local.dev-gutools.co.uk", "custom.local.dev"} {
		t.Run(baseDomain, func(t *testing.T) {
			chekovHostname := "chekov." + baseDomain
			serviceHostname := "xxx." + baseDomain
			routes := []nginx.Route{{Hostname: chekovHostname}, {Hostname: serviceHostname}}
			filtered := filterAliasHostnameRoutes(routes, chekovHostname)
			page := renderAliasPage(routeHostnames(filtered), nil, nil)
			if strings.Contains(page, `<option value="`+chekovHostname+`"`) {
				t.Fatal("Chekov appears in mapped domains")
			}
			if !strings.Contains(page, `<option value="`+serviceHostname+`"`) {
				t.Fatal("service missing from mapped domains")
			}
			if len(routes) != 2 {
				t.Fatal("filter changed original routes")
			}
			page = renderAliasPage(routeHostnames(filterAliasHostnameRoutes(routes[:1], chekovHostname)), nil, nil)
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
	page := renderAliasPage([]string{routes[0].Hostname, routes[1].Hostname, routes[2].Hostname}, nil, suggestions)
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
	page := renderAliasPage([]string{"service.local.dev"}, nil, map[string][]string{
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
	page := renderAliasPage(routeHostnames(routes), nil, suggestions)
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
