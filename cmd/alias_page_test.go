package main

import (
	"strings"
	"testing"

	"github.com/dev-tools/chekov/internal/nginx"
)

func TestAliasPageExcludesChekovMappedDomain(t *testing.T) {
	for _, baseDomain := range []string{"local.dev-gutools.co.uk", "custom.local.dev"} {
		t.Run(baseDomain, func(t *testing.T) {
			chekovHostname := "chekov." + baseDomain
			serviceHostname := "xxx." + baseDomain
			routes := []nginx.Route{{Hostname: chekovHostname}, {Hostname: serviceHostname}}
			filtered := filterAliasHostnameRoutes(routes, chekovHostname)
			page := renderAliasPage(routeHostnames(filtered), nil)
			if strings.Contains(page, `<option value="`+chekovHostname+`">`) {
				t.Fatal("Chekov appears in mapped domains")
			}
			if !strings.Contains(page, `<option value="`+serviceHostname+`">`) {
				t.Fatal("service missing from mapped domains")
			}
			if len(routes) != 2 {
				t.Fatal("filter changed original routes")
			}
			page = renderAliasPage(routeHostnames(filterAliasHostnameRoutes(routes[:1], chekovHostname)), nil)
			if !strings.Contains(page, `<option value="">No mapped domains</option>`) {
				t.Fatal("Chekov-only routes should show no mapped domains")
			}
		})
	}
}
