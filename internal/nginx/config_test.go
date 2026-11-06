package nginx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
