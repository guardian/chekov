package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRefreshAWSProfilesValidatesCredentials(t *testing.T) {
	dir := t.TempDir()
	aws := `#!/bin/sh
if [ "$1" != sts ] || [ "$2" != get-caller-identity ] || [ "$3" != --profile ]; then
  echo "unexpected AWS command" >&2
  exit 2
fi
case "$4" in
  expired)
    echo "ExpiredToken: The security token included in the request is expired" >&2
    exit 1
    ;;
  unavailable)
    echo "Could not connect to the endpoint URL" >&2
    exit 1
    ;;
esac
printf '{"Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test","UserId":"test"}\n'
`
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(aws), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := KnownGoodAWSProfiles()
	t.Cleanup(func() {
		awsProfileState.mu.Lock()
		awsProfileState.profiles = previous
		awsProfileState.mu.Unlock()
	})

	tests := []struct {
		name        string
		credentials string
		wantGood    []string
		wantError   string
	}{
		{name: "valid", credentials: "[valid]\naws_access_key_id=test\n", wantGood: []string{"valid"}},
		{name: "expired", credentials: "[expired]\naws_access_key_id=test\n", wantError: "ExpiredToken"},
		{name: "mixed", credentials: "[valid]\naws_access_key_id=test\n[expired]\naws_access_key_id=test\n", wantGood: []string{"valid"}, wantError: `profile "expired"`},
		{name: "connection failure", credentials: "[unavailable]\naws_access_key_id=test\n", wantError: "Could not connect to the endpoint URL"},
		{name: "no profiles"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials")
			if err := os.WriteFile(path, []byte(test.credentials), 0o600); err != nil {
				t.Fatal(err)
			}
			good, err := refreshAWSProfilesFromCredentials(path)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			if !reflect.DeepEqual(good, test.wantGood) {
				t.Fatalf("valid profiles = %v, want %v", good, test.wantGood)
			}
			if !reflect.DeepEqual(KnownGoodAWSProfiles(), test.wantGood) {
				t.Fatalf("cached profiles = %v, want %v", KnownGoodAWSProfiles(), test.wantGood)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "[expired]") || strings.Contains(string(data), "[unavailable]") {
				t.Fatalf("invalid credentials were not removed")
			}
			if len(test.wantGood) > 0 && !strings.Contains(string(data), "[valid]") {
				t.Fatal("valid credentials were removed")
			}
		})
	}
	t.Run("read failure", func(t *testing.T) {
		if _, err := refreshAWSProfilesFromCredentials(t.TempDir()); err == nil {
			t.Fatal("expected credentials read error")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		good, err := refreshAWSProfilesFromCredentials(filepath.Join(t.TempDir(), "missing"))
		if err != nil || len(good) != 0 {
			t.Fatalf("missing file: profiles=%v, error=%v", good, err)
		}
	})
}
