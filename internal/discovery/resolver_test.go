package discovery

import "testing"

func TestResolveRouteSharedNetworks(t *testing.T) {
	tests := []struct {
		name      string
		proxy     map[string]NetworkInfo
		container map[string]NetworkInfo
		published map[string][]PortBinding
		wantHost  string
		wantPort  int
		wantNet   string
	}{
		{
			name:      "default bridge uses container IP",
			proxy:     map[string]NetworkInfo{"bridge": {IPAddress: "172.17.0.2"}},
			container: map[string]NetworkInfo{"bridge": {IPAddress: "172.17.0.4"}},
			wantHost:  "172.17.0.4",
			wantPort:  9000,
			wantNet:   "bridge",
		},
		{
			name:      "user-defined network preferred over bridge",
			proxy:     map[string]NetworkInfo{"bridge": {}, "app-net": {}},
			container: map[string]NetworkInfo{"bridge": {IPAddress: "172.17.0.4"}, "app-net": {IPAddress: "172.18.0.4"}},
			wantHost:  "app",
			wantPort:  9000,
			wantNet:   "app-net",
		},
		{
			name:      "shared host network falls back to published port",
			proxy:     map[string]NetworkInfo{"host": {}},
			container: map[string]NetworkInfo{"host": {}},
			published: map[string][]PortBinding{"9000/tcp": {{HostPort: "9001"}}},
			wantHost:  "host.docker.internal",
			wantPort:  9001,
		},
		{
			name:      "missing bridge IP falls back to published port",
			proxy:     map[string]NetworkInfo{"bridge": {}},
			container: map[string]NetworkInfo{"bridge": {}},
			published: map[string][]PortBinding{"9000/tcp": {{HostPort: "9001"}}},
			wantHost:  "host.docker.internal",
			wantPort:  9001,
		},
		{
			name:      "invalid bridge IP without fallback is unreachable",
			proxy:     map[string]NetworkInfo{"bridge": {}},
			container: map[string]NetworkInfo{"bridge": {IPAddress: "invalid"}},
		},
		{
			name:      "unshared bridge is unreachable",
			proxy:     map[string]NetworkInfo{"other": {}},
			container: map[string]NetworkInfo{"bridge": {IPAddress: "172.17.0.4"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewNetworkResolver("proxy")
			resolver.SetProxyNetworks(tt.proxy)
			route := resolver.ResolveRoute("app", "id", "app.example.com", 9000, tt.container, tt.published)
			if tt.wantHost == "" {
				if route != nil {
					t.Fatalf("got route %+v, want nil", route)
				}
				return
			}
			if route == nil {
				t.Fatal("got nil route")
			}
			if route.TargetHost != tt.wantHost || route.TargetPort != tt.wantPort || route.Network != tt.wantNet {
				t.Errorf("got route %+v, want host %q port %d network %q", route, tt.wantHost, tt.wantPort, tt.wantNet)
			}
		})
	}
}
