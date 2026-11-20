package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dev-tools/chekov/internal/discovery"
)

func TestProxyIdentityResolvesShortDockerHostname(t *testing.T) {
	const hostname = "1acb6f39e107"
	self := discovery.ContainerInfo{
		ID:              hostname + "4db1d218d97d05dba308a301823c096ecf63c0d4f900abcabf1a",
		Name:            "inner-chekov",
		NetworkSettings: map[string]discovery.NetworkInfo{"bridge": {}},
		PublishedPorts:  map[string][]discovery.PortBinding{"443": {{HostPort: "8443"}}},
	}
	id, networks, ports := resolveProxyIdentity(context.Background(), func() (string, error) {
		return hostname, nil
	}, func(_ context.Context, lookup string) (*discovery.ContainerInfo, error) {
		if lookup != hostname {
			t.Fatalf("Docker lookup = %q, want %q", lookup, hostname)
		}
		return &self, nil
	})
	if id != self.ID || !reflect.DeepEqual(networks, self.NetworkSettings) || !isProxyContainer(&self, id) {
		t.Fatalf("resolved ID = %q, networks = %v; expected inner-chekov to be skipped", id, networks)
	}
	if ports.https != 8443 {
		t.Fatalf("discovered HTTPS port = %d, want 8443", ports.https)
	}
	other := discovery.ContainerInfo{
		ID:     "different-container",
		Name:   "chekov",
		Labels: map[string]string{"com.docker.compose.service": "chekov"},
	}
	if isProxyContainer(&other, id) {
		t.Fatal("a different container must not be skipped based on its name or Compose label")
	}
}

func TestProxyIdentityDoesNotGuessWhenInspectFails(t *testing.T) {
	id, networks, _ := resolveProxyIdentity(context.Background(), func() (string, error) {
		return "custom-hostname", nil
	}, func(context.Context, string) (*discovery.ContainerInfo, error) {
		return nil, errors.New("container not found")
	})
	if id != "" || networks != nil || isProxyContainer(&discovery.ContainerInfo{}, id) {
		t.Fatalf("failed inspection should leave identity unresolved, got %q and %v", id, networks)
	}
}
