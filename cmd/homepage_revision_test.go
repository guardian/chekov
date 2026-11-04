package main

import (
	"testing"

	"github.com/dev-tools/chekov/internal/nginx"
)

func TestHomepageRevisionTracksRoutesAndDisplayedData(t *testing.T) {
	rows := []ContainerDiagnostic{{Name: "app", Status: "routed"}}
	endpoints := []PublishedEndpointRow{{Hostname: "app.local.dev", Status: "running"}}
	routes := []nginx.Route{{Hostname: "app.local.dev", TargetHost: "127.0.0.1", TargetPort: 3000}}
	initial := homepageRevision(rows, endpoints, routes)
	if got := homepageRevision(rows, endpoints, append([]nginx.Route(nil), routes...)); got != initial {
		t.Fatalf("unchanged data changed revision: %d != %d", got, initial)
	}
	for _, change := range []struct {
		name      string
		rows      []ContainerDiagnostic
		endpoints []PublishedEndpointRow
		routes    []nginx.Route
	}{
		{"alias added", rows, endpoints, append(append([]nginx.Route(nil), routes...), nginx.Route{Hostname: "alias.local.dev"})},
		{"route removed", rows, endpoints, nil},
		{"tunnel target changed", rows, endpoints, []nginx.Route{{Hostname: "app.local.dev", TargetHost: "127.0.0.1", TargetPort: 4000}}},
		{"tunnel status changed", rows, []PublishedEndpointRow{{Hostname: "app.local.dev", Status: "stopped"}}, routes},
		{"diagnostics changed", []ContainerDiagnostic{{Name: "app", Status: "skipped"}}, endpoints, routes},
	} {
		t.Run(change.name, func(t *testing.T) {
			if got := homepageRevision(change.rows, change.endpoints, change.routes); got == initial {
				t.Fatal("changed data did not change revision")
			}
		})
	}
	if homepageRevision(nil, nil, nil) == homepageRevision(rows, endpoints, routes) {
		t.Fatal("first route did not change empty homepage revision")
	}
}
