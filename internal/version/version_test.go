package version

import "testing"

func TestDevSentinelDefault(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("expected default Version to be the dev sentinel, got %q", Version)
	}
}
