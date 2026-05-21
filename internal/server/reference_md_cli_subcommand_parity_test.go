package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #1509 v0.83 (closes the drift surfaced in #672 workstream 4):
// assert every subcommand advertised in `pincher --help`'s banner
// (cmd/pinch/main.go printHelpBanner) has a dedicated
// `### `pincher <name>`` section in docs/REFERENCE.md.
//
// Pre-fix the help banner advertised 16 subcommands but REFERENCE.md
// had dedicated sections for only 11. The five missing (supervised,
// health-check, stats, hook-stats, verify) created a "are these
// supported?" question for users reading the docs entry point and
// forced them back to `pincher <cmd> --help`.
//
// Complementary to TestReferenceMD_EveryRegisteredToolMentioned which
// pins the MCP-tool side. This test pins the CLI-subcommand side.
//
// Source-of-truth list is hardcoded below. Adding a subcommand to
// the help banner requires bumping this list AND adding a section to
// REFERENCE.md — the test enforces lockstep so the docs entry point
// can never silently drift from the CLI surface again.

// expectedCLISubcommands enumerates the subcommand names that
// cmd/pinch/main.go's printHelpBanner advertises. Keep in sync.
//
// Exclusions: `--http :PORT` and `--version` are flags on the
// no-subcommand form, not subcommands. `hook-check` is in code but
// intentionally excluded from --help.
var expectedCLISubcommands = []string{
	"index",
	"supervised",
	"health-check",
	"doctor",
	"self-test",
	"rebuild-fts",
	"stats",
	"hook-stats",
	"bench",
	"update",
	"web",
	"init",
	"project",
	"verify",
	"vacuum",
}

func TestReferenceMD_EveryCLISubcommandHasSection(t *testing.T) {
	t.Parallel()

	refBytes, err := os.ReadFile("../../docs/reference/cli.md")
	if err != nil {
		t.Fatalf("read docs/reference/cli.md: %v", err)
	}
	ref := string(refBytes)

	// We look for the precise `### `pincher <name>`` heading shape,
	// not just a backticked mention — many subcommands are name-dropped
	// in prose without having their own section. The h3 + backticks
	// is the canonical "this subcommand is documented" marker.
	for _, sub := range expectedCLISubcommands {
		heading := "### `pincher " + sub + "`"
		if !strings.Contains(ref, heading) {
			t.Errorf("CLI subcommand %q has no dedicated `### \\`pincher %s\\`` section in docs/reference/cli.md — add one (see existing sections for the standard shape) or remove the subcommand from cmd/pinch/main.go printHelpBanner if intentionally hidden",
				sub, sub)
		}
	}
}

// TestReferenceMD_NoOrphanCLISection — inverse gate: every
// `### `pincher <name>`` section in REFERENCE.md must correspond
// to a subcommand on the expected list. Catches the drift direction
// where a subcommand is removed from the help banner but the docs
// section sticks around as a confusing residue.
//
// `pincher init --git-hooks` is a sub-mode of `init` and is exempt
// (its heading shape `### `pincher init --git-hooks`` doesn't match
// the regex's `\w+` group).
func TestReferenceMD_NoOrphanCLISection(t *testing.T) {
	t.Parallel()

	refBytes, err := os.ReadFile("../../docs/reference/cli.md")
	if err != nil {
		t.Fatalf("read docs/reference/cli.md: %v", err)
	}
	ref := string(refBytes)

	known := make(map[string]bool, len(expectedCLISubcommands))
	for _, s := range expectedCLISubcommands {
		known[s] = true
	}

	// Match `### `pincher <single-token>`` headings, where single-token
	// is a kebab-or-letter identifier (no spaces — that excludes
	// the `pincher init --git-hooks` sub-mode heading).
	headingRE := regexp.MustCompile("(?m)^### `pincher ([a-z][a-z\\-]*)`\\s*$")
	for _, m := range headingRE.FindAllStringSubmatch(ref, -1) {
		name := m[1]
		if !known[name] {
			t.Errorf("docs/reference/cli.md has `### \\`pincher %s\\`` but %q is not on expectedCLISubcommands — either add it to the help banner + this test, or remove the orphan section",
				name, name)
		}
	}
}
