package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-tools/chekov/internal/nginx"
)

func testConnectivityRoute(t *testing.T, address string) nginx.Route {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return nginx.Route{Hostname: "app.local.dev", TargetHost: u.Hostname(), TargetPort: port, Protocol: u.Scheme}
}

func TestProbeConnectivityHTTPResponses(t *testing.T) {
	for _, status := range []int{200, 302, 401, 403, 404, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "app.local.dev" || r.URL.Path != "/" {
					t.Errorf("unexpected request: %s %s", r.Host, r.URL)
				}
				w.Header().Set("Location", "http://must-not-follow.invalid/")
				w.WriteHeader(status)
			}))
			defer server.Close()
			result := probeConnectivity(context.Background(), testConnectivityRoute(t, server.URL))
			if result.Summary != fmt.Sprintf("Responding - HTTP %d", status) || result.CheckedAt.IsZero() {
				t.Fatalf("unexpected result: %#v", result)
			}
		})
	}
}

func TestProbeConnectivityFailures(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		route := testConnectivityRoute(t, "http://"+listener.Addr().String())
		listener.Close()
		if got := probeConnectivity(context.Background(), route); got.Summary != "Connection refused" {
			t.Fatalf("unexpected result: %#v", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if got := probeConnectivity(ctx, testConnectivityRoute(t, server.URL)); got.Summary != "Connection timed out" {
			t.Fatalf("unexpected result: %#v", got)
		}
	})
	t.Run("TLS verification", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		route := testConnectivityRoute(t, server.URL)
		route.VerifySSL = true
		if got := probeConnectivity(context.Background(), route); got.Summary != "TLS failed" {
			t.Fatalf("unexpected result: %#v", got)
		}
		route.VerifySSL = false
		if got := probeConnectivity(context.Background(), route); got.Summary != "Responding - HTTP 200" {
			t.Fatalf("unexpected result without verification: %#v", got)
		}
	})
}

func TestConnectivityAliasesAndInactiveTunnels(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	route := testConnectivityRoute(t, server.URL)
	alias := route
	alias.Hostname = "alias.local.dev"
	stopped := route
	stopped.Hostname = "tunnel.local.dev"
	stopped.ContainerName = "aws-tunnel-saved"
	manager := NewTunnelManager()
	manager.tunnels["saved"] = &TunnelEntry{ID: "saved", Status: "stopped"}
	monitor := &ConnectivityMonitor{results: make(map[connectivityKey]ConnectivityResult)}
	routes := []nginx.Route{route, alias, stopped}
	if got := monitor.snapshot(routes, manager)[route.Hostname].Summary; got != "Checking..." {
		t.Fatalf("unexpected initial status: %s", got)
	}
	monitor.check(context.Background(), routes, manager)
	results := monitor.snapshot(routes, manager)
	if calls.Load() != 1 || results[route.Hostname] != results[alias.Hostname] {
		t.Fatalf("aliases did not share one check: calls=%d results=%#v", calls.Load(), results)
	}
	if results[stopped.Hostname].Summary != "Tunnel stopped" {
		t.Fatalf("unexpected stopped result: %#v", results[stopped.Hostname])
	}
	monitor.check(context.Background(), []nginx.Route{stopped}, manager)
	if calls.Load() != 1 || len(monitor.results) != 0 {
		t.Fatal("inactive tunnel was probed or removed routes retained")
	}
}

func TestRenderHomepageConnectivity(t *testing.T) {
	page := renderHomepageHTML(nil, 1, []PublishedEndpointRow{{
		Hostname: "app.local.dev",
		Connectivity: ConnectivityResult{
			Summary: "Connection refused", Detail: "<error>", CheckedAt: time.Now(),
		},
	}}, "Nginx rejected <config>")
	for _, want := range []string{
		"<th>Connectivity</th>", `data-connectivity-host="app.local.dev"`,
		"Connection refused", "&lt;error&gt;", "Last checked:",
		"Nginx rejected &lt;config&gt;",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	empty := renderHomepageHTML(nil, 1, nil, "")
	if !strings.Contains(empty, `colspan="3"`) || !strings.Contains(empty, `role="alert" hidden`) {
		t.Fatal("empty table or hidden error banner incorrect")
	}
}

func TestConnectivityStaleResults(t *testing.T) {
	route := nginx.Route{Hostname: "app.local.dev", TargetHost: "127.0.0.1", TargetPort: 8080}
	monitor := &ConnectivityMonitor{results: map[connectivityKey]ConnectivityResult{
		routeConnectivityKey(route): {
			Summary: "Responding - HTTP 200", CheckedAt: time.Now().Add(-time.Minute),
		},
	}}
	if got := monitor.snapshot([]nginx.Route{route}, nil)[route.Hostname]; !strings.HasPrefix(got.Summary, "Stale - ") {
		t.Fatalf("old result not marked stale: %#v", got)
	}
}

func TestConnectivityBoundsConcurrentChecks(t *testing.T) {
	var active, peak, calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for previous := peak.Load(); current > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	var routes []nginx.Route
	for range 10 {
		server := httptest.NewServer(handler)
		defer server.Close()
		routes = append(routes, testConnectivityRoute(t, server.URL))
	}
	monitor := &ConnectivityMonitor{results: make(map[connectivityKey]ConnectivityResult)}
	monitor.check(context.Background(), routes, nil)
	if peak.Load() > 4 || calls.Load() != 10 {
		t.Fatalf("unexpected concurrency or coverage: peak=%d calls=%d", peak.Load(), calls.Load())
	}
}
