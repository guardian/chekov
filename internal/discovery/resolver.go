package discovery

import (
	"fmt"
	"log"
	"net"
	"slices"
	"strings"
)

// ResolvedRoute represents a resolved route from proxy to application.
type ResolvedRoute struct {
	ContainerName string
	ContainerID   string
	Hostname      string
	TargetHost    string // e.g., "myapp" or "172.20.0.2" or "host.docker.internal"
	TargetPort    int
	Network       string // Optional: the network used for connectivity
}

// NetworkResolver resolves network connectivity between proxy and containers.
type NetworkResolver struct {
	proxyContainerID string
	proxyNetworks    map[string]NetworkInfo
}

// NewNetworkResolver creates a new network resolver.
func NewNetworkResolver(proxyContainerID string) *NetworkResolver {
	return &NetworkResolver{
		proxyContainerID: proxyContainerID,
		proxyNetworks:    make(map[string]NetworkInfo),
	}
}

// SetProxyNetworks stores proxy container's network information.
func (nr *NetworkResolver) SetProxyNetworks(networks map[string]NetworkInfo) {
	nr.proxyNetworks = networks
	log.Printf("Proxy networks: %v\n", mapKeys(networks))
}

// ResolveRoute resolves how to reach an application container.
// Strategy:
// 1. Use container DNS on a shared user-defined network
// 2. Use the container's IP on the default bridge
// 3. Fall back to a host port if available
// 4. Return nil if unreachable
func (nr *NetworkResolver) ResolveRoute(
	containerName string,
	containerID string,
	hostname string,
	appPort int,
	containerNetworks map[string]NetworkInfo,
	publishedPorts map[string][]PortBinding,
) *ResolvedRoute {
	sharedNetworks := nr.findSharedNetworks(containerNetworks)
	slices.Sort(sharedNetworks)
	for _, networkName := range sharedNetworks {
		if isDefaultNetwork(networkName) {
			continue
		}
		route := &ResolvedRoute{
			ContainerName: containerName,
			ContainerID:   containerID,
			Hostname:      hostname,
			TargetHost:    containerName,
			TargetPort:    appPort,
			Network:       networkName,
		}
		log.Printf(
			"Resolved route via shared network %s: %s -> %s:%d\n",
			networkName, hostname, containerName, appPort,
		)
		return route
	}

	if slices.Contains(sharedNetworks, "bridge") {
		ip := net.ParseIP(containerNetworks["bridge"].IPAddress)
		if ip != nil && ip.To4() != nil && !ip.IsUnspecified() {
			route := &ResolvedRoute{
				ContainerName: containerName,
				ContainerID:   containerID,
				Hostname:      hostname,
				TargetHost:    ip.String(),
				TargetPort:    appPort,
				Network:       "bridge",
			}
			log.Printf("Resolved route via default bridge: %s -> %s:%d\n", hostname, route.TargetHost, appPort)
			return route
		}
		log.Printf("WARNING: No usable IPv4 address for %s on default bridge\n", containerName)
	}

	// Honor a hostname-prefixed host port such as 8082.example.com.
	if hostPort := extractHostnamePort(hostname); hostPort > 0 {
		route := &ResolvedRoute{
			ContainerName: containerName,
			ContainerID:   containerID,
			Hostname:      hostname,
			TargetHost:    "host.docker.internal",
			TargetPort:    hostPort,
			Network:       "",
		}
		log.Printf(
			"Resolved route via hostname port: %s -> host.docker.internal:%d\n",
			hostname, hostPort,
		)
		return route
	}

	hostPort := extractHostPort(publishedPorts, appPort)
	if hostPort > 0 {
		route := &ResolvedRoute{
			ContainerName: containerName,
			ContainerID:   containerID,
			Hostname:      hostname,
			TargetHost:    "host.docker.internal", // Docker Desktop magic hostname
			TargetPort:    hostPort,
			Network:       "",
		}
		log.Printf(
			"Resolved route via published port: %s -> host.docker.internal:%d\n",
			hostname, hostPort,
		)
		return route
	}

	log.Printf(
		"WARNING: Unable to resolve route for %s: no usable shared network or published port\n",
		containerName,
	)
	return nil
}

// findSharedNetworks finds networks shared by proxy and container.
func (nr *NetworkResolver) findSharedNetworks(containerNetworks map[string]NetworkInfo) []string {
	var shared []string
	for networkName := range containerNetworks {
		if _, ok := nr.proxyNetworks[networkName]; ok {
			shared = append(shared, networkName)
			log.Printf("Found shared network: %s\n", networkName)
		}
	}
	return shared
}

// isDefaultNetwork checks if a network is a default Docker network.
func isDefaultNetwork(name string) bool {
	defaultNets := []string{"bridge", "host", "none"}
	return slices.Contains(defaultNets, name)
}

// extractHostPort extracts host port mapping for container port.
// Returns 0 if not found.
func extractHostPort(publishedPorts map[string][]PortBinding, containerPort int) int {
	for _, tcpKey := range []string{
		fmt.Sprintf("%d/tcp", containerPort),
		fmt.Sprintf("%d", containerPort),
	} {
		if bindings, ok := publishedPorts[tcpKey]; ok {
			if len(bindings) > 0 && bindings[0].HostPort != "" {
				var hostPort int
				fmt.Sscanf(bindings[0].HostPort, "%d", &hostPort)
				return hostPort
			}
		}
	}

	return 0
}

// extractHostnamePort extracts host port mapping for container port.
// Returns 0 if not found.
func extractHostnamePort(hostname string) int {
	hostname = strings.TrimSpace(hostname)
	hostname = strings.TrimPrefix(hostname, "http://")
	hostname = strings.TrimPrefix(hostname, "https://")
	if hostname == "" {
		return 0
	}
	first := strings.SplitN(hostname, ".", 2)[0]
	if first == "" {
		return 0
	}
	var port int
	if _, err := fmt.Sscanf(first, "%d", &port); err != nil || port <= 0 {
		return 0
	}
	return port
}

// mapKeys returns the keys of a map as a slice.
func mapKeys(m map[string]NetworkInfo) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
