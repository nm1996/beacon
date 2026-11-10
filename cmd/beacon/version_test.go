package main

import "testing"

func TestBuildVersionPrefersLinkTimeVersion(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "v9.9.9"

	if got := buildVersion(); got != "v9.9.9" {
		t.Errorf("expected the -ldflags version, got %q", got)
	}
}

func TestBuildVersionFallsBackToBuildInfo(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = ""

	if got := buildVersion(); got == "" {
		t.Error("expected a version from the build info, got an empty string")
	}
}
