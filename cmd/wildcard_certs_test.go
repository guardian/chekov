package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dev-tools/chekov/internal/nginx"
)

func TestAliasCreatesMissingWildcardCertificate(t *testing.T) {
	baseDomain := "local.dev-gutools.co.uk"
	scope := "zzz." + baseDomain
	for _, test := range []struct {
		name     string
		cert     bool
		key      bool
		wantMint bool
	}{
		{"missing pair", false, false, true},
		{"missing certificate", false, true, true},
		{"missing key", true, false, true},
		{"existing pair", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			certDir := t.TempDir()
			certPath, keyPath := wildcardCertPaths(scope, certDir)
			for path, exists := range map[string]bool{certPath: test.cert, keyPath: test.key} {
				if exists {
					if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			sourceRoutes := []nginx.Route{{
				Hostname: "xxx." + baseDomain, TargetHost: "127.0.0.1", TargetPort: 40000,
				CertPath: "/source.crt", KeyPath: "/source.key",
			}}
			manager := NewAliasManager()
			aliasHostname := "yyy." + scope
			if _, err := manager.AddAlias(sourceRoutes[0].Hostname, aliasHostname, baseDomain, sourceRoutes); err != nil {
				t.Fatal(err)
			}
			merged := manager.MergeRoutes(sourceRoutes)
			alias := findRouteByHostname(merged, aliasHostname)
			if alias == nil {
				t.Fatal("alias route missing")
			}
			minted := false
			routes := ensureRouteWildcardCertsInDir([]nginx.Route{*alias}, baseDomain, certDir,
				func(gotScope, gotCert, gotKey string) (bool, error) {
					if gotScope != scope || gotCert != certPath || gotKey != keyPath {
						t.Fatalf("unexpected mint arguments: %q, %q, %q", gotScope, gotCert, gotKey)
					}
					minted = true
					for _, path := range []string{gotCert, gotKey} {
						if err := os.WriteFile(path, []byte("minted fixture"), 0o600); err != nil {
							return false, err
						}
					}
					return true, nil
				})
			if minted != test.wantMint {
				t.Fatalf("minted = %v, want %v", minted, test.wantMint)
			}
			if routes[0].CertPath != certPath || routes[0].KeyPath != keyPath {
				t.Fatalf("alias did not bind its scope certificate: %+v", routes[0])
			}
		})
	}
}

func TestWildcardCertificateMintFailurePreservesRouteTLS(t *testing.T) {
	routes := []nginx.Route{{
		Hostname: "yyy.zzz.local.dev-gutools.co.uk",
		CertPath: "/source.crt", KeyPath: "/source.key",
	}}
	routes = ensureRouteWildcardCertsInDir(routes, "local.dev-gutools.co.uk", t.TempDir(),
		func(string, string, string) (bool, error) {
			return false, fmt.Errorf("mkcert unavailable")
		})
	if routes[0].CertPath != "/source.crt" || routes[0].KeyPath != "/source.key" {
		t.Fatalf("mint failure changed route TLS: %+v", routes[0])
	}
}

func TestHostnameWildcardScope(t *testing.T) {
	baseDomain := "local.dev-gutools.co.uk"
	for _, test := range []struct {
		hostname string
		want     string
	}{
		{"xxx.local.dev-gutools.co.uk", baseDomain},
		{"yyy.zzz.local.dev-gutools.co.uk", "zzz." + baseDomain},
		{"aaa.yyy.zzz.local.dev-gutools.co.uk", "yyy.zzz." + baseDomain},
		{"outside.example.com", ""},
	} {
		t.Run(test.hostname, func(t *testing.T) {
			if got := hostnameWildcardScope(test.hostname, baseDomain); got != test.want {
				t.Fatalf("scope = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAliasUsesOwnWildcardCertificateAfterMerge(t *testing.T) {
	baseDomain := "local.dev-gutools.co.uk"
	sourceHostname := "xxx." + baseDomain
	aliasHostname := "yyy.zzz." + baseDomain
	certDir := t.TempDir()
	t.Setenv("DEVCONTAINER_NGINX_CERT_DIR", certDir)
	sourceCert, sourceKey := wildcardCertPaths(baseDomain, certDir)
	aliasCert, aliasKey := wildcardCertPaths("zzz."+baseDomain, certDir)
	for _, path := range []string{sourceCert, sourceKey, aliasCert, aliasKey} {
		if err := os.WriteFile(path, []byte("existing certificate fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sourceRoutes := []nginx.Route{{
		Hostname: sourceHostname, TargetHost: "127.0.0.1", TargetPort: 40000,
		CertPath: sourceCert, KeyPath: sourceKey,
	}}
	manager := NewAliasManager()
	if _, err := manager.AddAlias(sourceHostname, aliasHostname, baseDomain, sourceRoutes); err != nil {
		t.Fatal(err)
	}
	configGen := nginx.NewConfigGenerator("", sourceCert, sourceKey)

	// Both immediate creation and later reconciliation must resolve TLS after merging.
	for _, phase := range []string{"creation", "reconciliation"} {
		t.Run(phase, func(t *testing.T) {
			routes := ensureRouteWildcardCerts(manager.MergeRoutes(sourceRoutes), baseDomain)
			alias := findRouteByHostname(routes, aliasHostname)
			if alias == nil {
				t.Fatal("alias route missing")
			}
			if alias.CertPath != aliasCert || alias.KeyPath != aliasKey {
				t.Fatalf("alias TLS = (%q, %q), want (%q, %q)", alias.CertPath, alias.KeyPath, aliasCert, aliasKey)
			}
			if alias.TargetHost != "127.0.0.1" || alias.TargetPort != 40000 {
				t.Fatalf("alias upstream changed: %+v", alias)
			}
			config := configGen.GenerateConfig([]nginx.Route{*alias})
			for _, want := range []string{
				"server_name " + aliasHostname + ";",
				"ssl_certificate " + aliasCert + ";",
				"ssl_certificate_key " + aliasKey + ";",
			} {
				if !strings.Contains(config, want) {
					t.Fatalf("alias config missing %q", want)
				}
			}
		})
	}
}
