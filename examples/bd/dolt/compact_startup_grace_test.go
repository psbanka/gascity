package dolt_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Startup grace (ga-u0m): the mol-dog-compactor cooldown order becomes due
// the moment a city that was down longer than the interval starts, which
// lands the flatten exactly in the post-start write burst — agents waking
// and committing session heartbeats. The 2026-08-25 incident fired six
// minutes after `gc start`, raced a bead write that landed 264ms after the
// flatten commit, and quarantined hq. The flatten pass must defer while the
// managed dolt server is younger than GC_DOLT_COMPACT_STARTUP_GRACE_SECS,
// read from the same runtime state file port resolution already validates.
// The operator reclaim paths (--gc-only, bare GC) and a state file without
// a usable started_at must NOT defer.

// rewriteCompactRuntimeStartedAt replaces the fixture's managed runtime
// state (whose fixture default started_at is months old) with one whose
// started_at is the given timestamp, keeping pid/port/data_dir intact so
// port resolution still validates the file.
func rewriteCompactRuntimeStartedAt(t *testing.T, fixture compactScriptFixture, startedAt string) {
	t.Helper()
	payload := fmt.Sprintf(`{"running":true,"pid":%d,"port":%d,"data_dir":%q,"started_at":%q}`,
		os.Getpid(), fixture.port, fixture.dataDir, startedAt)
	writeCompactRuntimeState(t, fixture, payload)
}

func writeCompactRuntimeState(t *testing.T, fixture compactScriptFixture, payload string) {
	t.Helper()
	statePath := filepath.Join(fixture.cityPath, ".gc", "runtime", "packs", "dolt", "dolt-state.json")
	if err := os.WriteFile(statePath, []byte(payload), 0o644); err != nil {
		t.Fatalf("rewrite dolt runtime state: %v", err)
	}
}

func readCompactDoltLog(t *testing.T, fixture compactScriptFixture) string {
	t.Helper()
	data, err := os.ReadFile(fixture.doltLog)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read dolt log: %v", err)
	}
	return string(data)
}

func TestCompactScriptDefersFlattenWithinStartupGrace(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	rewriteCompactRuntimeStartedAt(t, fixture, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	out, err := fixture.run(t, "success", "GC_DOLT_COMPACT_THRESHOLD_COMMITS=500")
	if err != nil {
		t.Fatalf("startup-grace defer must exit 0 (a skip, not a failure): %v\n%s", err, out)
	}
	if !strings.Contains(out, "within startup grace") {
		t.Fatalf("output missing startup-grace defer message:\n%s", out)
	}
	log := readCompactDoltLog(t, fixture)
	for _, forbidden := range []string{"DOLT_RESET", "DOLT_COMMIT", "DOLT_GC"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("deferred flatten pass must not issue %s:\n%s", forbidden, log)
		}
	}
}

func TestCompactScriptStartupGraceZeroDisablesDefer(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	rewriteCompactRuntimeStartedAt(t, fixture, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	out, err := fixture.run(t, "success",
		"GC_DOLT_COMPACT_THRESHOLD_COMMITS=500",
		"GC_DOLT_COMPACT_STARTUP_GRACE_SECS=0")
	if err != nil {
		t.Fatalf("compact failed with grace disabled: %v\n%s", err, out)
	}
	if strings.Contains(out, "within startup grace") {
		t.Fatalf("grace 0 must disable the defer:\n%s", out)
	}
	log := readCompactDoltLog(t, fixture)
	for _, want := range []string{"DOLT_RESET", "DOLT_COMMIT", "DOLT_GC"} {
		if !strings.Contains(log, want) {
			t.Fatalf("dolt log missing %s with grace disabled:\n%s", want, log)
		}
	}
}

func TestCompactScriptStartupGraceInvalidValueFailsLoudly(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	out, err := fixture.run(t, "success",
		"GC_DOLT_COMPACT_THRESHOLD_COMMITS=500",
		"GC_DOLT_COMPACT_STARTUP_GRACE_SECS=soon")
	if err == nil {
		t.Fatalf("invalid grace value must fail, not be silently ignored:\n%s", out)
	}
	if !strings.Contains(out, "invalid GC_DOLT_COMPACT_STARTUP_GRACE_SECS") {
		t.Fatalf("output missing invalid-value diagnostic:\n%s", out)
	}
}

func TestCompactScriptGCOnlyIgnoresStartupGrace(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	rewriteCompactRuntimeStartedAt(t, fixture, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	out, err := fixture.runWithArgs(t, "success", []string{"--gc-only"},
		"GC_DOLT_COMPACT_THRESHOLD_COMMITS=500")
	if err != nil {
		t.Fatalf("gc-only reclaim failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "within startup grace") {
		t.Fatalf("gc-only is an operator recovery path and must not defer on startup grace:\n%s", out)
	}
	if !strings.Contains(readCompactDoltLog(t, fixture), "DOLT_GC") {
		t.Fatalf("gc-only must still reclaim:\n%s", readCompactDoltLog(t, fixture))
	}
}

func TestCompactScriptBareGCIgnoresStartupGrace(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	rewriteCompactRuntimeStartedAt(t, fixture, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	out, err := fixture.run(t, "below_threshold",
		"GC_DOLT_COMPACT_THRESHOLD_COMMITS=500",
		"GC_DOLT_COMPACT_BARE_GC=1")
	if err != nil {
		t.Fatalf("bare-gc compact failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "within startup grace") {
		t.Fatalf("bare GC is the memory-pressure path and must not defer on startup grace:\n%s", out)
	}
	if !strings.Contains(readCompactDoltLog(t, fixture), "CALL DOLT_GC()") {
		t.Fatalf("bare-gc must still issue CALL DOLT_GC():\n%s", readCompactDoltLog(t, fixture))
	}
}

func TestCompactScriptStartupGraceFailsOpenWithoutStartedAt(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	writeCompactRuntimeState(t, fixture, fmt.Sprintf(
		`{"running":true,"pid":%d,"port":%d,"data_dir":%q}`,
		os.Getpid(), fixture.port, fixture.dataDir))
	out, err := fixture.run(t, "success", "GC_DOLT_COMPACT_THRESHOLD_COMMITS=500")
	if err != nil {
		t.Fatalf("compact failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "proceeding without startup grace") {
		t.Fatalf("fail-open must be reported, not silent:\n%s", out)
	}
	if !strings.Contains(readCompactDoltLog(t, fixture), "DOLT_RESET") {
		t.Fatalf("missing started_at must fail open to the normal flatten:\n%s", readCompactDoltLog(t, fixture))
	}
}

func TestCompactScriptStartupGraceFailsOpenOnUnparseableStartedAt(t *testing.T) {
	fixture := newCompactScriptFixture(t)
	rewriteCompactRuntimeStartedAt(t, fixture, "not-a-timestamp")
	out, err := fixture.run(t, "success", "GC_DOLT_COMPACT_THRESHOLD_COMMITS=500")
	if err != nil {
		t.Fatalf("compact failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "proceeding without startup grace") {
		t.Fatalf("fail-open must be reported, not silent:\n%s", out)
	}
	if !strings.Contains(readCompactDoltLog(t, fixture), "DOLT_RESET") {
		t.Fatalf("unparseable started_at must fail open to the normal flatten:\n%s", readCompactDoltLog(t, fixture))
	}
}
