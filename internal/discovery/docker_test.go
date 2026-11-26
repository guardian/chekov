package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestDockerClientOperations(t *testing.T) {
	const summary = `{
		"Id":"test-container","Names":["/example"],"Image":"example:latest","ImageID":"sha256:example",
		"Ports":[
			{"PrivatePort":8080,"PublicPort":18080,"IP":"127.0.0.1","Type":"tcp"},
			{"PrivatePort":8080,"PublicPort":18080,"IP":"::1","Type":"tcp"},
			{"PrivatePort":9090,"Type":"tcp"}
		],
		"Labels":{"project":"example"},"NetworkSettings":{"Networks":{"test-network":{}}}
	}`
	const inspect = `{
		"Id":"test-container","Name":"/example","Image":"sha256:example","State":{"Running":true},
		"Config":{"Labels":{"project":"example"},"ExposedPorts":{"8080/tcp":{},"53/udp":{}}},
		"HostConfig":{"PortBindings":{"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"18080"},{"HostIp":"","HostPort":"28080"}]}},
		"NetworkSettings":{"Networks":{
			"test-network":{"Gateway":"172.18.0.1","IPAddress":"172.18.0.2"},
			"no-address":{}
		}}
	}`
	networkRequests := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1.40/containers/json":
			fmt.Fprintf(w, "[%s]", summary)
		case "GET /v1.40/containers/test-container/json":
			fmt.Fprint(w, inspect)
		case "GET /v1.40/events":
			var filters map[string]map[string]bool
			if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
				t.Errorf("invalid events filters: %v", err)
			} else if !filters["type"]["container"] || !filters["event"]["start"] || filters["event"]["exec_start"] || filters["event"]["health_status"] {
				t.Errorf("unexpected events filters: %v", filters)
			}
			fmt.Fprintln(w, `{"Type":"container","Action":"start","Actor":{"ID":"test-container"}}`)
		case "POST /v1.40/networks/test-network/connect", "POST /v1.40/networks/test-network/disconnect":
			var body struct {
				Container string
				Force     bool
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.Force {
				t.Error("network disconnect must not be forced")
			}
			networkRequests[r.URL.Path] = body.Container
			fmt.Fprint(w, "{}")
		default:
			t.Errorf("unexpected Docker request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.40"))
	if err != nil {
		t.Fatal(err)
	}
	dc := &DockerClient{client: c}
	defer dc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	containers, err := dc.ListContainers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ContainerInfo{{
		ID: "test-container", Name: "example", Image: "sha256:example", Running: true,
		Labels:       map[string]string{"project": "example"},
		ExposedPorts: []int{8080, 9090}, Networks: []string{"test-network"},
		PublishedPorts: map[string][]PortBinding{
			"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "18080"}, {HostIP: "::1", HostPort: "18080"}},
		},
	}}
	if !reflect.DeepEqual(containers, want) {
		t.Fatalf("ListContainers = %+v, want %+v", containers, want)
	}

	info, err := dc.InspectContainer(ctx, "test-container")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "example" || info.Image != "sha256:example" || !info.Running ||
		!reflect.DeepEqual(info.ExposedPorts, []int{8080}) || info.Labels["project"] != "example" {
		t.Fatalf("unexpected inspected container: %+v", info)
	}
	if !reflect.DeepEqual(info.PublishedPorts["8080"], []PortBinding{
		{HostIP: "127.0.0.1", HostPort: "18080"}, {HostPort: "28080"},
	}) {
		t.Fatalf("unexpected inspected port bindings: %+v", info.PublishedPorts)
	}
	if info.NetworkSettings["test-network"] != (NetworkInfo{Gateway: "172.18.0.1", IPAddress: "172.18.0.2"}) ||
		info.NetworkSettings["no-address"] != (NetworkInfo{}) {
		t.Fatalf("unexpected inspected networks: %+v", info.NetworkSettings)
	}

	messages, _ := dc.WatchEvents(ctx)
	select {
	case message := <-messages:
		if message.Type != "container" || message.Action != "start" || message.Actor.ID != "test-container" {
			t.Fatalf("unexpected Docker event: %+v", message)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := dc.ConnectToNetwork(ctx, "test-container", "test-network"); err != nil {
		t.Fatal(err)
	}
	if err := dc.DisconnectFromNetwork(ctx, "test-container", "test-network"); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"connect", "disconnect"} {
		if networkRequests["/v1.40/networks/test-network/"+action] != "test-container" {
			t.Errorf("network %s did not target test-container", action)
		}
	}
}

func TestNewDockerClientNegotiatesAPIVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.40")
		switch r.URL.Path {
		case "/_ping":
			fmt.Fprint(w, "OK")
		case "/v1.40/containers/json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "[]")
		default:
			t.Errorf("unexpected Docker request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", server.URL)
	t.Setenv("DOCKER_API_VERSION", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	dc, err := NewDockerClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	if _, err := dc.ListContainers(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDockerClientReportsDaemonErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"daemon unavailable"}`)
	}))
	defer server.Close()
	c, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.40"))
	if err != nil {
		t.Fatal(err)
	}
	dc := &DockerClient{client: c}
	defer dc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	checks := []func() error{
		func() error { _, err := dc.ListContainers(ctx); return err },
		func() error { _, err := dc.InspectContainer(ctx, "test-container"); return err },
		func() error { return dc.ConnectToNetwork(ctx, "test-container", "test-network") },
		func() error { return dc.DisconnectFromNetwork(ctx, "test-container", "test-network") },
		func() error {
			_, errors := dc.WatchEvents(ctx)
			select {
			case err := <-errors:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	for i, check := range checks {
		if err := check(); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
			t.Errorf("operation %d: expected daemon error, got %v", i, err)
		}
	}
}

func TestDockerClientIntegration(t *testing.T) {
	if os.Getenv("CHEKOV_DOCKER_INTEGRATION") != "1" {
		t.Skip("set CHEKOV_DOCKER_INTEGRATION=1 to check discovery against a running Docker daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dc, err := NewDockerClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	containers, err := dc.ListContainers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range containers {
		info, err := dc.InspectContainer(ctx, summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		if info.ID != summary.ID || info.Name != summary.Name {
			t.Fatalf("list and inspect disagree: %+v, %+v", summary, info)
		}
	}
	t.Logf("discovered and inspected %d running containers", len(containers))
}
