package main

import (
	"net"
	"net/http"
	"strconv"

	"github.com/dev-tools/chekov/internal/discovery"
)

type dashboardPorts struct {
	http, https int
}

const ambiguousPort = -1

func discoverDashboardPorts(bindings map[string][]discovery.PortBinding) dashboardPorts {
	return dashboardPorts{http: uniquePublishedPort(bindings["80"]), https: uniquePublishedPort(bindings["443"])}
}

func uniquePublishedPort(bindings []discovery.PortBinding) int {
	var port int
	for _, binding := range bindings {
		n, err := strconv.Atoi(binding.HostPort)
		if err != nil || n <= 0 || n > 65535 {
			continue
		}
		if port != 0 && port != n {
			return ambiguousPort
		}
		port = n
	}
	return port
}

func (p dashboardPorts) forRequest(r *http.Request) dashboardPorts {
	obviousPort := 0
	if _, rawPort, err := net.SplitHostPort(r.Host); err == nil {
		if port, err := strconv.Atoi(rawPort); err == nil && port > 0 && port <= 65535 {
			obviousPort = port
		}
	}
	switch {
	case r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https":
		if obviousPort == 0 {
			obviousPort = 443
		}
		p.https = obviousPort
	case r.Header.Get("X-Forwarded-Proto") == "http":
		if obviousPort == 0 {
			obviousPort = 80
		}
		p.http = obviousPort
	}
	return p
}

func (p dashboardPorts) preferredServiceURL(host string) string {
	if p.https == 0 && p.http > 0 {
		return p.serviceURL("http", host)
	}
	if link := p.serviceURL("https", host); link != "" {
		return link
	}
	return p.serviceURL("http", host)
}

func (p dashboardPorts) serviceURL(scheme, host string) string {
	port, defaultPort := p.https, 443
	if scheme == "http" {
		port, defaultPort = p.http, 80
	}
	if port == ambiguousPort {
		return ""
	}
	if port != 0 && port != defaultPort {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return scheme + "://" + host
}
