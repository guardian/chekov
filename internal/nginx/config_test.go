package nginx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyPreservesAuthorityOnlyForChekovBackend(t *testing.T) {
	generator := NewConfigGenerator("", "", "")
	config := generator.renderConfig(nil)
	for _, line := range []string{"map $http_host $chekov_request_authority {", "default $http_host;", `"" $host;`} {
		if !strings.Contains(config, line) {
			t.Errorf("missing authority mapping: %s", line)
		}
	}
	ui := Route{Hostname: "ui-alias.local.dev", TargetHost: "127.0.0.1", TargetPort: 18080}
	app := Route{Hostname: "app.local.dev", TargetHost: "app", TargetPort: 18080, Protocol: "https"}
	uiConfig := strings.Join(generator.generateServerBlock(ui), "\n")
	appConfig := strings.Join(generator.generateServerBlock(app), "\n")
	if strings.Contains(uiConfig, "proxy_set_header Host $host;") {
		t.Fatal("UI alias should preserve the authority on HTTP and HTTPS")
	}
	if strings.Count(appConfig, "proxy_set_header Host $host;") != 2 || !strings.Contains(appConfig, "proxy_ssl_name $host;") {
		t.Fatal("service Host and TLS server name should remain hostname-only")
	}
}

func TestWildcardRouteConfig(t *testing.T) {
	generator := NewConfigGenerator("", "/default.crt", "/default.key")
	config := strings.Join(generator.generateServerBlock(Route{Hostname: "*.s3.app.local.dev", TargetHost: "app", TargetPort: 8080}), "\n")
	for _, want := range []string{
		"upstream upstream_wildcard_s3_app_local_dev {",
		"proxy_pass http://upstream_wildcard_s3_app_local_dev;",
		"server_name *.s3.app.local.dev;",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("wildcard config missing %q:\n%s", want, config)
		}
	}
}

func TestRoutingErrorLifecycle(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"$NGINX_TEST_FAILURE\" ]; then\n echo 'simulated nginx error' >&2\n exit 1\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, "nginx"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	generator := NewConfigGenerator(filepath.Join(dir, "generated.conf"), "", "")
	t.Setenv("NGINX_TEST_FAILURE", "-t")
	if generator.ValidateConfig() || !strings.Contains(generator.RoutingError(), "simulated nginx error") {
		t.Fatal("validation failure not exposed")
	}
	t.Setenv("NGINX_TEST_FAILURE", "-s")
	if !generator.ValidateConfig() || generator.RoutingError() == "" {
		t.Fatal("validation alone cleared the activation error")
	}
	if generator.ReloadNginx() || !strings.Contains(generator.RoutingError(), "Failed to reload") {
		t.Fatal("reload failure not exposed")
	}
	t.Setenv("NGINX_TEST_FAILURE", "")
	if !generator.ReloadNginx() || generator.RoutingError() != "" {
		t.Fatal("successful reload did not clear the error")
	}
}

func TestRoutingWriteFailure(t *testing.T) {
	generator := NewConfigGenerator(filepath.Join(t.TempDir(), "missing", "generated.conf"), "", "")
	if err := generator.ApplyRoutes(nil); err == nil || !strings.Contains(generator.RoutingError(), "Failed to write") {
		t.Fatal("write failure not exposed")
	}
}
