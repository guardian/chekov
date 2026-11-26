package main

import (
	"testing"

	"github.com/dev-tools/chekov/internal/discovery"
	"github.com/dev-tools/chekov/internal/nginx"
)

func TestDockerAliasLabelsUseContainerPorts(t *testing.T) {
	const domain = "local.dev"
	info := &discovery.ContainerInfo{
		Name:         "demo",
		ExposedPorts: []int{8080, 9000},
		PublishedPorts: map[string][]discovery.PortBinding{
			"8080": {{HostPort: "18080"}},
			"9000": {{HostPort: "19000"}},
		},
		Labels: map[string]string{
			"chekov.aliases":       "ambiguous",
			"chekov.aliases.8080":  "preview, api.demo.local.dev, invalid/name",
			"chekov.aliases.9000":  "admin",
			"chekov.aliases.18080": "wrong-port",
		},
	}
	for _, shared := range []bool{false, true} {
		resolver := discovery.NewNetworkResolver("proxy")
		expectedHost, apiPort, adminPort := "host.docker.internal", 18080, 19000
		if shared {
			info.NetworkSettings = map[string]discovery.NetworkInfo{"app-net": {}}
			resolver.SetProxyNetworks(info.NetworkSettings)
			expectedHost, apiPort, adminPort = "demo", 8080, 9000
		}
		routes, row := diagnoseContainer(info, discovery.NewIdentifier(), resolver, domain)
		merged := addHomepageAndAliasRoutes(routes, domain, NewAliasManager(), []ContainerDiagnostic{row})
		if len(routes) != 2 || len(merged) != 6 {
			t.Fatalf("shared=%t: unexpected primary or alias routes: %+v", shared, merged)
		}
		for name, port := range map[string]int{"preview.local.dev": apiPort, "api.demo.local.dev": apiPort, "admin.local.dev": adminPort} {
			if route := findRouteByHostname(merged, name); route == nil || route.TargetHost != expectedHost || route.TargetPort != port || route.AutoAliasSource == "" {
				t.Errorf("shared=%t: %s has unexpected route: %+v", shared, name, route)
			}
		}
	}
}

func TestDockerAliasLabelsAcceptLeadingWildcard(t *testing.T) {
	const domain = "local.dev"
	app := nginx.Route{Hostname: "app.local.dev", ContainerName: "app", TargetHost: "app", TargetPort: 8080}
	aliases := aliasesFromLabels("app", map[string]string{
		"chekov.aliases.8080": "*.s3.app, *, a.*.b, *x.app",
	}, []labelAliasTarget{{route: app, ports: []int{8080}}}, domain)
	if len(aliases) != 1 || aliases[0].AliasHostname != "*.s3.app.local.dev" {
		t.Fatalf("only the leading wildcard alias should be accepted: %+v", aliases)
	}
	merged := addHomepageAndAliasRoutes([]nginx.Route{app}, domain, NewAliasManager(),
		[]ContainerDiagnostic{{LabeledAliases: aliases}})
	if route := findRouteByHostname(merged, "*.s3.app.local.dev"); route == nil || route.TargetHost != app.TargetHost || route.TargetPort != app.TargetPort {
		t.Fatalf("wildcard alias route missing: %+v", merged)
	}
}

func TestNormalizeAliasHostnameWildcards(t *testing.T) {
	const domain = "local.dev"
	for raw, want := range map[string]string{
		"*.s3.app":           "*.s3.app.local.dev",
		"*.s3.app.local.dev": "*.s3.app.local.dev",
		"*.app":              "*.app.local.dev",
	} {
		if got, err := normalizeAliasHostname(raw, domain); err != nil || got != want {
			t.Errorf("normalizeAliasHostname(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"*", "*.local.dev", "a.*.b", "*.*.app", "*x.app", "x*.app"} {
		if got, err := normalizeAliasHostname(raw, domain); err == nil {
			t.Errorf("normalizeAliasHostname(%q) = %q; want error", raw, got)
		}
	}
}

func TestDockerAliasLabelsForChekovAndConflicts(t *testing.T) {
	const domain = "local.dev"
	ui := homepageRoute(domain)
	uiAliases := aliasesFromLabels("inner-chekov", map[string]string{
		"chekov.aliases":    "inner-chekov, taken, disputed",
		"chekov.aliases.80": "inner-chekov",
	}, []labelAliasTarget{{route: ui, ports: []int{80, 443}}}, domain)
	app := nginx.Route{Hostname: "taken.local.dev", ContainerName: "app", TargetHost: "app", TargetPort: 8080}
	appAliases := aliasesFromLabels("app", map[string]string{"chekov.aliases": "disputed"},
		[]labelAliasTarget{{route: app, ports: []int{8080}}}, domain)
	rows := []ContainerDiagnostic{{LabeledAliases: uiAliases}, {LabeledAliases: appAliases}}
	manager := NewAliasManager()
	merged := addHomepageAndAliasRoutes([]nginx.Route{app}, domain, manager, rows)
	if len(merged) != 3 || findRouteByHostname(merged, "disputed.local.dev") != nil {
		t.Fatalf("conflicting labels should not add routes: %+v", merged)
	}
	if route := findRouteByHostname(merged, app.Hostname); route == nil || *route != app {
		t.Fatal("label replaced a primary route")
	}
	if route := findRouteByHostname(merged, "inner-chekov.local.dev"); route == nil ||
		route.TargetHost != ui.TargetHost || route.TargetPort != ui.TargetPort || peerServiceRoute(*route) {
		t.Fatalf("UI alias should route directly to the controller and not be exported: %+v", route)
	}
	rebuilt := manager.mergeAllAliases(merged, nil, domain)
	if len(rebuilt) != 2 {
		t.Fatalf("removed label should remove its alias on rebuild: %+v", rebuilt)
	}
}
