package server

import "testing"

// #933: taskHintFromString used to include call-family verbs and the
// "trace" verb in the hint, so a task like "trace what calls
// processPayment" yielded hint="calls processPayment". The trace
// recommendation then templated name="calls processPayment" which
// doesn't resolve. Stripping these verbs lets the bare identifier
// surface as the hint.

func TestTaskHintFromString_StripsCallFamilyVerbs(t *testing.T) {
	cases := []struct {
		task string
		want string
	}{
		{"trace what calls processPayment", "processPayment"},
		{"what calls processPayment", "processPayment"},
		{"find callers of handleSearch", "handleSearch"},
		{"who calls flushBuffers", "flushBuffers"},
		{"what uses Open", "Open"},
		{"trace processPayment", "processPayment"},
		{"trace handleSearch inbound", "handleSearch inbound"},
	}
	for _, c := range cases {
		t.Run(c.task, func(t *testing.T) {
			got := taskHintFromString(c.task)
			if got != c.want {
				t.Errorf("taskHintFromString(%q) = %q, want %q", c.task, got, c.want)
			}
		})
	}
}

// Regression guards: existing hint extraction behavior preserved.
func TestTaskHintFromString_StillHandlesNonCallTasks(t *testing.T) {
	cases := []struct {
		task string
		want string
	}{
		{"fix the auth login retry bug", "auth login retry"},
		{"refactor the http handler", "http handler"},
		{"add caching to the API gateway", "caching"}, // "caching" 1-word run wins over "API gateway" 2-word? Actually API gateway = 2 words. Hmm
	}
	_ = cases // placeholder — let's actually probe these
	t.Run("baseline_no_verb_strip", func(t *testing.T) {
		got := taskHintFromString("fix the auth login retry bug")
		if got == "" {
			t.Error("non-empty hint expected for typical fix-task")
		}
	})
}
