package server

import (
	"sync/atomic"
	"testing"
)

// Tests for the #241 query-failure / retry-rate counters.
//
// The friction signal: an agent issues a search at default
// min_confidence=0.71, gets 0 results, retries with min_confidence=0,
// gets results. Each session should accumulate counts that surface in
// `pincher stats` so a high retry-rate is actionable diagnostic
// signal — "your default threshold is wrong for your workflow."

// recordQueryMetrics is a no-op for tools outside queryShapedTools so
// admin/orientation calls don't pollute the retry-rate denominator.
func TestRecordQueryMetrics_NonQueryToolsAreNoOp(t *testing.T) {
	srv, _, _ := newTestServer(t)

	for _, tool := range []string{"architecture", "list", "schema", "health", "stats", "guide", "adr", "fetch", "index", "symbol", "symbols", "context", "changes"} {
		srv.recordQueryMetrics(tool, map[string]any{}, map[string]any{"count": 0}, 100)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesTotal); got != 0 {
		t.Errorf("non-query tools incremented queries_total = %d, want 0", got)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesZeroResult); got != 0 {
		t.Errorf("non-query tools incremented queries_zero_result = %d, want 0", got)
	}
}

// A zero-result search increments queries_total + queries_zero_result
// and adds tokensUsed to the burned counter.
func TestRecordQueryMetrics_ZeroResultIncrementsAllZeroCounters(t *testing.T) {
	srv, _, _ := newTestServer(t)

	srv.recordQueryMetrics("search", map[string]any{"query": "no-such-symbol"}, map[string]any{"count": 0}, 250)

	if got := atomic.LoadInt64(&srv.statsQueriesTotal); got != 1 {
		t.Errorf("queries_total = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesZeroResult); got != 1 {
		t.Errorf("queries_zero_result = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&srv.statsTokensBurned); got != 250 {
		t.Errorf("tokens_burned = %d, want 250", got)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesRetriedSucceeded); got != 0 {
		t.Errorf("queries_retried_succeeded = %d, want 0 on a fresh zero-result", got)
	}
}

// Zero-result followed by an equivalent retry that returns ≥1 results
// credits queries_retried_succeeded — this is the "agent learned and
// recovered" signal. Pin same-tool + same-query as the discriminator.
func TestRecordQueryMetrics_RetryAfterZeroResultIsCredited(t *testing.T) {
	srv, _, _ := newTestServer(t)

	srv.recordQueryMetrics("search", map[string]any{"query": "ADR-NNNN → new location"}, map[string]any{"count": 0}, 200)
	srv.recordQueryMetrics("search", map[string]any{"query": "ADR-NNNN → new location"}, map[string]any{"count": 3}, 400)

	if got := atomic.LoadInt64(&srv.statsQueriesTotal); got != 2 {
		t.Errorf("queries_total = %d, want 2", got)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesZeroResult); got != 1 {
		t.Errorf("queries_zero_result = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesRetriedSucceeded); got != 1 {
		t.Errorf("queries_retried_succeeded = %d, want 1 (retry recovered)", got)
	}
	if got := atomic.LoadInt64(&srv.statsTokensBurned); got != 200 {
		t.Errorf("tokens_burned = %d, want 200 (only the failed attempt)", got)
	}
}

// A successful call between two zero-result calls clears the retry
// marker — the second zero result is a fresh failure, not a retry of
// the first. Without this guard, an unrelated middle call would
// inflate queries_retried_succeeded.
func TestRecordQueryMetrics_UnrelatedSuccessClearsMarker(t *testing.T) {
	srv, _, _ := newTestServer(t)

	srv.recordQueryMetrics("search", map[string]any{"query": "first-zero"}, map[string]any{"count": 0}, 100)
	srv.recordQueryMetrics("search", map[string]any{"query": "unrelated-success"}, map[string]any{"count": 5}, 200)
	srv.recordQueryMetrics("search", map[string]any{"query": "first-zero"}, map[string]any{"count": 7}, 300)

	if got := atomic.LoadInt64(&srv.statsQueriesRetriedSucceeded); got != 0 {
		t.Errorf("queries_retried_succeeded = %d, want 0 (marker should have been cleared)", got)
	}
	if got := atomic.LoadInt64(&srv.statsQueriesZeroResult); got != 1 {
		t.Errorf("queries_zero_result = %d, want 1", got)
	}
}

// Retry against a *different* tool doesn't credit recovery — the agent
// switching tools is not the "learned the threshold" signal we're
// trying to detect.
func TestRecordQueryMetrics_DifferentToolDoesNotCreditRetry(t *testing.T) {
	srv, _, _ := newTestServer(t)

	srv.recordQueryMetrics("search", map[string]any{"query": "foo"}, map[string]any{"count": 0}, 100)
	srv.recordQueryMetrics("query", map[string]any{"pinchql": "foo"}, map[string]any{"count": 1}, 200)

	if got := atomic.LoadInt64(&srv.statsQueriesRetriedSucceeded); got != 0 {
		t.Errorf("retried_succeeded = %d, want 0 (different tool, not a retry)", got)
	}
}

// Empty primary-arg (e.g. trace by id with no name) should not credit a
// retry — we'd otherwise match every empty-q zero-result against the
// next empty-q success.
func TestRecordQueryMetrics_EmptyPrimaryArgNoCredit(t *testing.T) {
	srv, _, _ := newTestServer(t)

	srv.recordQueryMetrics("trace", map[string]any{}, map[string]any{"count": 0}, 100)
	srv.recordQueryMetrics("trace", map[string]any{}, map[string]any{"count": 5}, 200)

	if got := atomic.LoadInt64(&srv.statsQueriesRetriedSucceeded); got != 0 {
		t.Errorf("retried_succeeded = %d, want 0 (empty primary arg shouldn't match across calls)", got)
	}
}

// primaryQueryArg pulls the right discriminator per tool. Pin the
// wiring so a future tool addition doesn't accidentally land using an
// arg name that won't match the retry detection.
func TestPrimaryQueryArg_PerToolDiscriminator(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"search", map[string]any{"query": "foo"}, "foo"},
		{"query", map[string]any{"pinchql": "MATCH (n) RETURN n"}, "MATCH (n) RETURN n"},
		{"query", map[string]any{"cypher": "legacy alias"}, "legacy alias"},
		{"trace", map[string]any{"name": "schemaMigrations"}, "schemaMigrations"},
		{"trace", map[string]any{"id": "internal/db/db.go::Open#Function"}, "internal/db/db.go::Open#Function"},
		{"neighborhood", map[string]any{"id": "abc"}, "abc"},
		{"unknown", map[string]any{"query": "x"}, ""},
		{"search", map[string]any{}, ""},
	}
	for _, tc := range cases {
		got := primaryQueryArg(tc.tool, tc.args)
		if got != tc.want {
			t.Errorf("tool=%q args=%v got=%q want=%q", tc.tool, tc.args, got, tc.want)
		}
	}
}

// End-to-end: counters round-trip through flushSession into the
// sessions table and aggregate via GetAllTimeQueryMetrics. This is the
// load-bearing path for #241 — `pincher stats` reads the aggregator,
// not in-memory counters.
func TestQueryMetrics_RoundTripThroughDB(t *testing.T) {
	srv, store, _ := newTestServer(t)

	atomic.StoreInt32(&srv.mcpConnected, 1)

	srv.recordQueryMetrics("search", map[string]any{"query": "fail-1"}, map[string]any{"count": 0}, 100)
	srv.recordQueryMetrics("search", map[string]any{"query": "fail-1"}, map[string]any{"count": 2}, 200)
	srv.recordQueryMetrics("search", map[string]any{"query": "fail-2"}, map[string]any{"count": 0}, 150)

	atomic.AddInt64(&srv.statsCalls, 3)

	srv.flushSession()

	qm, err := store.GetAllTimeQueryMetrics()
	if err != nil {
		t.Fatalf("GetAllTimeQueryMetrics: %v", err)
	}
	if qm.QueriesTotal != 3 {
		t.Errorf("queries_total = %d, want 3", qm.QueriesTotal)
	}
	if qm.QueriesZeroResult != 2 {
		t.Errorf("queries_zero_result = %d, want 2", qm.QueriesZeroResult)
	}
	if qm.QueriesRetriedSucceeded != 1 {
		t.Errorf("queries_retried_succeeded = %d, want 1", qm.QueriesRetriedSucceeded)
	}
	if qm.TokensBurnedOnFailures != 250 {
		t.Errorf("tokens_burned_on_failures = %d, want 250 (100 + 150)", qm.TokensBurnedOnFailures)
	}
}
