package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dev-tools/chekov/internal/discovery"
)

func TestDashboardLinksUsePublishedPorts(t *testing.T) {
	discovered := discoverDashboardPorts(map[string][]discovery.PortBinding{
		"80":  {{HostPort: "80"}},
		"443": {{HostIP: "0.0.0.0", HostPort: "8443"}, {HostIP: "::", HostPort: "8443"}},
	})
	for _, tt := range []struct {
		name, authority, scheme, httpURL, httpsURL string
	}{
		{"HTTP dashboard", "chekov.local.dev", "http", "http://app.local.dev", "https://app.local.dev:8443"},
		{"HTTPS dashboard", "chekov.local.dev:8443", "https", "http://app.local.dev", "https://app.local.dev:8443"},
		{"browser port takes precedence", "chekov.local.dev:9443", "https", "http://app.local.dev", "https://app.local.dev:9443"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tt.authority+"/", nil)
			r.Header.Set("X-Forwarded-Proto", tt.scheme)
			ports := discovered.forRequest(r)
			home := renderHomepageHTML(r.Host, nil, 1, []PublishedEndpointRow{{Hostname: "app.local.dev"}}, "", ports)
			if !strings.Contains(home, `href="`+tt.httpsURL+`"`) || !strings.Contains(home, `data-connectivity-host="app.local.dev"`) {
				t.Fatal("homepage should use the public HTTPS port without changing the route key")
			}
			debug := renderRouteLinks("app.local.dev -> host:9000", ports)
			for _, link := range []string{tt.httpURL, tt.httpsURL} {
				if !strings.Contains(debug, `href="`+link+`"`) {
					t.Errorf("debug page missing link %s", link)
				}
			}
		})
	}
}

func TestHomepagePrefersPublishedProtocol(t *testing.T) {
	for _, tt := range []struct {
		name      string
		bindings  map[string][]discovery.PortBinding
		dashboard string
		scheme    string
		want      string
	}{
		{"HTTP only", map[string][]discovery.PortBinding{"80": {{HostPort: "8080"}}}, "chekov.local.dev:8080", "http", "http://app.local.dev:8080"},
		{"default HTTP only", map[string][]discovery.PortBinding{"80": {{HostPort: "80"}}}, "chekov.local.dev", "http", "http://app.local.dev"},
		{"HTTPS only", map[string][]discovery.PortBinding{"443": {{HostPort: "8443"}}}, "chekov.local.dev:8443", "https", "https://app.local.dev:8443"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tt.dashboard+"/", nil)
			r.Header.Set("X-Forwarded-Proto", tt.scheme)
			ports := discoverDashboardPorts(tt.bindings).forRequest(r)
			page := renderHomepageHTML(r.Host, nil, 1, []PublishedEndpointRow{{Hostname: "app.local.dev"}}, "", ports)
			if !strings.Contains(page, `href="`+tt.want+`"`) {
				t.Fatalf("homepage missing service link %s", tt.want)
			}
		})
	}
}

func TestDashboardLinksDoNotGuessAmbiguousPort(t *testing.T) {
	ports := discoverDashboardPorts(map[string][]discovery.PortBinding{
		"443": {{HostPort: "8443"}, {HostPort: "9443"}},
	})
	r := httptest.NewRequest("GET", "http://chekov.local.dev:8080/", nil)
	r.Header.Set("X-Forwarded-Proto", "http")
	ports = ports.forRequest(r)
	page := renderHomepageHTML(r.Host, nil, 1, []PublishedEndpointRow{{Hostname: "app.local.dev"}}, "", ports)
	if !strings.Contains(page, `href="http://app.local.dev:8080"`) || strings.Contains(page, `href="https://app.local.dev`) {
		t.Fatal("expected a same-protocol link instead of guessing the HTTPS port")
	}
}
