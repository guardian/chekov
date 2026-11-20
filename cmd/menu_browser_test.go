package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportMenuBrowserFixtures(t *testing.T) {
	dir := os.Getenv("CHEKOV_MENU_FIXTURE_DIR")
	if dir == "" {
		t.Skip("browser fixture export only")
	}

	pages := map[string]string{
		"home.html":                renderHomepageHTML("", nil, 1, nil, "", dashboardPorts{}),
		"aws-config.html":          renderAWSConfigPage("", AWSConfig{}, false),
		"tunnels.html":             renderTunnelPage("", AWSConfig{}, nil, nil),
		"aliases.html":             renderAliasPage("", nil, nil, nil),
		"shell-launch.html":        renderShellLaunchPage(""),
		"remote-shell-launch.html": renderRemoteShellLaunchPage(""),
		"shell.html":               renderShellPage("", "", ""),
		"remote-shell.html":        renderRemoteShellPage("", "", "", "", "", ""),
	}
	for name, content := range pages {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
