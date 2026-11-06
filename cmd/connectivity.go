package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dev-tools/chekov/internal/nginx"
)

type ConnectivityResult struct {
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail"`
	CheckedAt time.Time `json:"checkedAt"`
}

type connectivityKey struct {
	Host, Protocol, VerifyName string
	Port                       int
	VerifySSL                  bool
}

func routeConnectivityKey(route nginx.Route) connectivityKey {
	protocol := route.Protocol
	if protocol == "" {
		protocol = "http"
	}
	verifyName := route.VerifyName
	if protocol == "https" && verifyName == "" {
		verifyName = route.Hostname
	}
	return connectivityKey{route.TargetHost, protocol, verifyName, route.TargetPort, route.VerifySSL}
}

type ConnectivityMonitor struct {
	mu      sync.RWMutex
	results map[connectivityKey]ConnectivityResult
}

func newConnectivityMonitor(ctx context.Context, routes func() []nginx.Route, tunnels *TunnelManager) *ConnectivityMonitor {
	m := &ConnectivityMonitor{results: make(map[connectivityKey]ConnectivityResult)}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			m.check(ctx, routes(), tunnels)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return m
}

func inactiveTunnelResults(tunnels *TunnelManager) map[string]ConnectivityResult {
	out := make(map[string]ConnectivityResult)
	if tunnels != nil {
		for _, entry := range tunnels.Snapshot() {
			if status := tunnelEntryStatus(&entry); status != "running" {
				out["aws-tunnel-"+entry.ID] = ConnectivityResult{
					Summary: "Tunnel " + status,
					Detail:  "Start the tunnel before checking connectivity.",
				}
			}
		}
	}
	return out
}

func (m *ConnectivityMonitor) check(ctx context.Context, routes []nginx.Route, tunnels *TunnelManager) {
	inactive := inactiveTunnelResults(tunnels)
	unique := make(map[connectivityKey]nginx.Route)
	for _, route := range routes {
		if _, stopped := inactive[route.ContainerName]; stopped || isInternalHomepageRoute(route.Hostname) {
			continue
		}
		key := routeConnectivityKey(route)
		if _, exists := unique[key]; !exists {
			unique[key] = route
		}
	}
	results := make(map[connectivityKey]ConnectivityResult, len(unique))
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for key, route := range unique {
		select {
		case limit <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(key connectivityKey, route nginx.Route) {
			defer wg.Done()
			defer func() { <-limit }()
			result := probeConnectivity(ctx, route)
			mu.Lock()
			results[key] = result
			mu.Unlock()
		}(key, route)
	}
	wg.Wait()
	m.mu.Lock()
	m.results = results
	m.mu.Unlock()
}

func (m *ConnectivityMonitor) snapshot(routes []nginx.Route, tunnels *TunnelManager) map[string]ConnectivityResult {
	inactive := inactiveTunnelResults(tunnels)
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]ConnectivityResult)
	for _, route := range routes {
		result, found := m.results[routeConnectivityKey(route)]
		if !found {
			result = ConnectivityResult{Summary: "Checking...", Detail: "Waiting for the first background check."}
		}
		if stopped, ok := inactive[route.ContainerName]; ok {
			result = stopped
		} else if !result.CheckedAt.IsZero() && time.Since(result.CheckedAt) > 45*time.Second {
			result.Summary = "Stale - " + result.Summary
		}
		out[route.Hostname] = result
	}
	return out
}

func probeConnectivity(ctx context.Context, route nginx.Route) ConnectivityResult {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	key := routeConnectivityKey(route)
	transport := &http.Transport{
		// Match Nginx's upstream TLS policy, not the public endpoint certificate.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !key.VerifySSL, ServerName: key.VerifyName},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	address := (&url.URL{Scheme: key.Protocol, Host: net.JoinHostPort(key.Host, strconv.Itoa(key.Port)), Path: "/"}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return ConnectivityResult{Summary: "Check failed", Detail: err.Error(), CheckedAt: time.Now()}
	}
	request.Host = route.Hostname
	response, err := client.Do(request)
	result := ConnectivityResult{CheckedAt: time.Now()}
	if err == nil {
		response.Body.Close()
		result.Summary = fmt.Sprintf("Responding - HTTP %d", response.StatusCode)
		result.Detail = "Backend returned " + response.Status + ". This checks connectivity, not application health."
		return result
	}
	result.Detail = err.Error()
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var hostname x509.HostnameError
	switch {
	case errors.As(err, &dns):
		result.Summary = "DNS failed"
	case errors.Is(err, context.DeadlineExceeded):
		result.Summary = "Connection timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		result.Summary = "Connection refused"
	case errors.As(err, &cert), errors.As(err, &record), errors.As(err, &hostname):
		result.Summary = "TLS failed"
	case strings.Contains(err.Error(), "tls:"):
		result.Summary = "TLS failed"
	default:
		result.Summary = "Connection failed"
	}
	return result
}
