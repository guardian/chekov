package main

import (
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/dev-tools/chekov/internal/discovery"
	"github.com/dev-tools/chekov/internal/nginx"
)

const aliasLabel = "chekov.aliases"

type labelAliasTarget struct {
	route nginx.Route
	ports []int
}

func containerAliasPorts(route *discovery.ResolvedRoute, appPort int, published map[string][]discovery.PortBinding) []int {
	if route.TargetHost == "host.docker.internal" && route.Network == "" {
		ports := discovery.ContainerPortsForHostPort(published, route.TargetPort)
		if len(ports) == 1 {
			return ports
		}
		if len(ports) > 1 || route.TargetPort != appPort {
			return nil
		}
	}
	return []int{appPort}
}

func aliasesFromLabels(container string, labels map[string]string, targets []labelAliasTarget, baseDomain string) []AliasEntry {
	var keys []string
	for key := range labels {
		if key == aliasLabel || strings.HasPrefix(key, aliasLabel+".") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var aliases []AliasEntry
	for _, key := range keys {
		var matches []nginx.Route
		if key == aliasLabel {
			if len(targets) == 1 {
				matches = append(matches, targets[0].route)
			}
		} else {
			suffix := strings.TrimPrefix(key, aliasLabel+".")
			port, err := strconv.Atoi(suffix)
			if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != suffix {
				log.Printf("WARNING: Ignoring alias label %q on %q: invalid container port", key, container)
				continue
			}
			for _, target := range targets {
				for _, candidate := range target.ports {
					if candidate != port {
						continue
					}
					// The plain-container fallback may make two names for the same backend.
					if len(matches) == 0 || matches[0].TargetHost != target.route.TargetHost ||
						matches[0].TargetPort != target.route.TargetPort {
						matches = append(matches, target.route)
					}
					break
				}
			}
		}
		if len(matches) != 1 {
			log.Printf("WARNING: Ignoring alias label %q on %q: does not identify a single route", key, container)
			continue
		}
		for _, raw := range strings.Split(labels[key], ",") {
			name, err := normalizeAliasHostname(raw, strings.ToLower(baseDomain))
			if err != nil || len(name) > 253 {
				log.Printf("WARNING: Ignoring invalid alias %q from label %q on %q", raw, key, container)
				continue
			}
			aliases = append(aliases, AliasEntry{SourceHostname: matches[0].Hostname, AliasHostname: name})
		}
	}
	return aliases
}
