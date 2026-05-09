package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPrintHelpBanner_ListsAllSubcommands pins the contract that
// `pincher --help` (which calls printHelpBanner) advertises every
// subcommand main.go dispatches to. If a future PR adds a subcommand
// without updating the banner, this test catches it — discoverability
// is the whole point of the banner.
func TestPrintHelpBanner_ListsAllSubcommands(t *testing.T) {
	var out bytes.Buffer
	printHelpBanner(&out)
	body := out.String()

	for _, sub := range []string{"index", "doctor", "self-test", "rebuild-fts", "stats", "--version", "--http"} {
		if !strings.Contains(body, sub) {
			t.Errorf("banner missing subcommand mention %q:\n%s", sub, body)
		}
	}
	// The banner should also include the "Usage:" header so flag's
	// PrintDefaults output reads as the flag list rather than a continuation.
	if !strings.Contains(body, "Usage:") {
		t.Errorf("banner missing 'Usage:' header:\n%s", body)
	}
}
