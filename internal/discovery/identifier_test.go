package discovery

import (
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeHostname(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"rendering.dotcom", "rendering.dotcom"},
		{"/My_App", "my-app"},
		{"-My App-", "myapp"},
		{"Rendering.Dotcom", "rendering.dotcom"},
		{"!!!", ""},
		{".rendering", ""},
		{"rendering.", ""},
		{"rendering..dotcom", ""},
		{"rendering.-dotcom", ""},
		{"rendering-.dotcom", ""},
		{strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{strings.Repeat("a", 64), ""},
		{strings.Repeat("a", 64) + ".dotcom", ""},
		{strings.Repeat("a", 63) + ".dotcom", strings.Repeat("a", 63) + ".dotcom"},
		{strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("b", 61),
			strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("b", 61)},
		{strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("b", 62), ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NewIdentifier().SanitizeHostname(test.name); got != test.want {
				t.Fatalf("SanitizeHostname() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExtractRouteTargetsFromPortLabels(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     []RouteTarget
	}{
		{
			name:     "single dotted label in metadata array",
			metadata: `[{"id":"feature"},{"forwardPorts":[3030],"portsAttributes":{"3030":{"label":"rendering.dotcom"}}}]`,
			want:     []RouteTarget{{ProjectName: "rendering.dotcom", ApplicationPort: 3030}},
		},
		{
			name:     "single label in metadata object",
			metadata: `{"forwardPorts":[3000],"portsAttributes":{"3000":{"label":"App"}}}`,
			want:     []RouteTarget{{ProjectName: "app", ApplicationPort: 3000}},
		},
		{
			name:     "multiple labels retain port order",
			metadata: `{"forwardPorts":[8080,3000],"portsAttributes":{"8080":{"label":"frontend"},"3000":{"label":"backend"}}}`,
			want: []RouteTarget{
				{ProjectName: "backend", ApplicationPort: 3000},
				{ProjectName: "frontend", ApplicationPort: 8080},
			},
		},
		{
			name:     "invalid label falls back to container name",
			metadata: `{"forwardPorts":[3030],"portsAttributes":{"3030":{"label":"rendering..dotcom"}}}`,
			want:     []RouteTarget{{ProjectName: "jolly-feistel", ApplicationPort: 3030}},
		},
		{
			name:     "unlabeled port retains container name",
			metadata: `{"forwardPorts":[3030]}`,
			want:     []RouteTarget{{ProjectName: "jolly-feistel", ApplicationPort: 3030}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := NewIdentifier().ExtractRouteTargets("jolly_feistel", map[string]string{
				"devcontainer.metadata": test.metadata,
			}, nil, nil)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ExtractRouteTargets() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSuggestProjectNames(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   []string
	}{
		{"vscode folder", map[string]string{
			"devcontainer.local_folder": "/Users/developer/code/dotcom-rendering",
			"devcontainer.metadata":     `[{"forwardPorts":[3030]}]`,
		}, []string{"dotcom-rendering"}},
		{"vscode metadata name", map[string]string{
			"devcontainer.metadata": `[{"name":"My App"},{"name":"My App"}]`,
		}, []string{"myapp"}},
		{"intellij", map[string]string{
			"com.intellij.devcontainer.presentable.name": "amiable",
			"com.intellij.devcontainer.initial.model":    `{"name":"amiable"}`,
			"com.intellij.devcontainer.model":            `{"forwardPorts":[9101]}`,
			"com.intellij.devcontainer.sources.path":     "/Users/developer/code/amiable/",
		}, []string{"amiable"}},
		{"distinct names sorted", map[string]string{
			"devcontainer.metadata":     `{"name":"Zulu"}`,
			"devcontainer.local_folder": "/work/alpha",
		}, []string{"alpha", "zulu"}},
		{"invalid metadata retains folder", map[string]string{
			"devcontainer.metadata":     `not json`,
			"devcontainer.local_folder": "/work/project",
		}, []string{"project"}},
		{"invalid names", map[string]string{
			"devcontainer.metadata":     `{"name":"!!!"}`,
			"devcontainer.local_folder": "/",
		}, []string{}},
		{"plain container", nil, []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id := NewIdentifier()
			if got := id.SuggestProjectNames(test.labels); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SuggestProjectNames() = %#v, want %#v", got, test.want)
			}
			if got := id.ExtractProjectName("vigorous_carver", test.labels); got != "vigorous-carver" {
				t.Fatalf("suggestions changed generated hostname: %s", got)
			}
		})
	}
}

func TestExtractApplicationPortFromDevcontainerMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     int
	}{
		{
			name:     "object",
			metadata: `{"forwardPorts":[3000]}`,
			want:     3000,
		},
		{
			name:     "array",
			metadata: `[{"name":"feature"},{"forwardPorts":[5173]}]`,
			want:     5173,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := NewIdentifier().ExtractApplicationPort(nil, nil, map[string]string{
				"devcontainer.metadata": test.metadata,
			})
			if got != test.want {
				t.Fatalf("ExtractApplicationPort() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestExtractPlainContainerFallbackRoutes(t *testing.T) {
	id := NewIdentifier()

	t.Run("single-port container", func(t *testing.T) {
		got := id.ExtractRouteTargets("zealous_chebyshev", nil, []int{80}, nil)
		want := []RouteTarget{{ProjectName: "zealous-chebyshev", ApplicationPort: 80}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ExtractRouteTargets() = %#v, want %#v", got, want)
		}
	})

	t.Run("multi-port container", func(t *testing.T) {
		got := id.ExtractRouteTargets("zealous_chebyshev", nil, []int{80, 8080}, nil)
		want := []RouteTarget{
			{ProjectName: "zealous-chebyshev", ApplicationPort: 8080},
			{ProjectName: "80.zealous-chebyshev", ApplicationPort: 80},
			{ProjectName: "8080.zealous-chebyshev", ApplicationPort: 8080},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ExtractRouteTargets() = %#v, want %#v", got, want)
		}
	})

	t.Run("multi-published-host-ports", func(t *testing.T) {
		published := map[string][]PortBinding{
			"80/tcp": {
				{HostPort: "8081"},
				{HostPort: "8082"},
			},
		}
		got := id.ExtractRouteTargets("hello-world", nil, []int{80}, published)
		want := []RouteTarget{
			{ProjectName: "8081.hello-world", ApplicationPort: 8081},
			{ProjectName: "8082.hello-world", ApplicationPort: 8082},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ExtractRouteTargets() = %#v, want %#v", got, want)
		}
	})
}
