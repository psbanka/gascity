package config

import (
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

// ApplyPoolLifecycleDefaults is the ga-r9s gate: an agent that declares no
// lifecycle key at all must not inherit "no idle checking at all", because a
// warm-idle session that never drains is exactly the shape that strands
// routed work (gc sling will not nudge a warm worker, and scale_check counts
// the warm session as satisfied demand). Provenance for the numbers and the
// mechanism is in the bead and openspec/changes/archive/2026-08-26-worker-pool-lifecycle.

func TestApplyPoolLifecycleDefaults_AgentDeclaringNothingGetsDefault(t *testing.T) {
	cfg := &City{Agents: []Agent{{Name: "worker", Dir: "rig"}}}
	ApplyPoolLifecycleDefaults(cfg)
	got := cfg.Agents[0].IdleTimeout
	if got != DefaultPoolIdleTimeout {
		t.Errorf("IdleTimeout = %q, want %q — an agent with no lifecycle policy must get the safe default", got, DefaultPoolIdleTimeout)
	}
}

func TestApplyPoolLifecycleDefaults_EachLifecycleKeyOptsOut(t *testing.T) {
	ptrInt := func(i int) *int { return &i }
	cases := map[string]func(*Agent){
		"idle_timeout":        func(a *Agent) { a.IdleTimeout = "1h" },
		"sleep_after_idle":    func(a *Agent) { a.SleepAfterIdle = "off" },
		"max_session_age":     func(a *Agent) { a.MaxSessionAge = "5h" },
		"wake_mode":           func(a *Agent) { a.WakeMode = "fresh" },
		"lifecycle":           func(a *Agent) { a.Lifecycle = "one_shot" },
		"scale_check":         func(a *Agent) { a.ScaleCheck = "echo 1" },
		"min_active_sessions": func(a *Agent) { a.MinActiveSessions = ptrInt(1) },
		"max_active_sessions": func(a *Agent) { a.MaxActiveSessions = ptrInt(3) },
	}
	for key, set := range cases {
		agent := Agent{Name: "worker", Dir: "rig"}
		set(&agent)
		cfg := &City{Agents: []Agent{agent}}
		ApplyPoolLifecycleDefaults(cfg)
		if got := cfg.Agents[0].IdleTimeout; got == DefaultPoolIdleTimeout {
			t.Errorf("%s: agent declaring this key must keep Empty idle_timeout, got the default — partial policies are deliberate choices, not omissions", key)
		}
	}
}

func TestApplyPoolLifecycleDefaults_NamedSessionBackedAgentSkipped(t *testing.T) {
	cfg := &City{
		Agents:        []Agent{{Name: "deacon", Dir: "gascity"}},
		NamedSessions: []NamedSession{{Name: "deacon", Template: "deacon", Dir: "gascity"}},
	}
	ApplyPoolLifecycleDefaults(cfg)
	if got := cfg.Agents[0].IdleTimeout; got != "" {
		t.Errorf("IdleTimeout = %q, want Empty — a named session owns this agent's lifecycle", got)
	}
}

func TestApplyPoolLifecycleDefaults_ControlDispatcherSkipped(t *testing.T) {
	cfg := &City{Agents: []Agent{{Name: ControlDispatcherAgentName}}}
	ApplyPoolLifecycleDefaults(cfg)
	if got := cfg.Agents[0].IdleTimeout; got != "" {
		t.Errorf("IdleTimeout = %q, want Empty — the control dispatcher's lifecycle is SDK infrastructure, not pool policy", got)
	}
}

func TestApplyPoolLifecycleDefaults_ExplicitIdleTimeoutPreserved(t *testing.T) {
	cfg := &City{Agents: []Agent{{Name: "worker", IdleTimeout: "15m"}}}
	ApplyPoolLifecycleDefaults(cfg)
	if got := cfg.Agents[0].IdleTimeout; got != "15m" {
		t.Errorf("IdleTimeout = %q, want 15m — user-supplied values must win", got)
	}
}

// Wiring: the default must flow through the real composition path, not just
// the direct call above. A polecat-shaped agent declared in a pack with no
// lifecycle keys (the gascity/roles shape from the incident) loads composed
// with the safe default.
func TestApplyPoolLifecycleDefaults_LoadComposesDefaultForPlainWorker(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "city.toml", `
[workspace]
name = "test"

[providers.codex]
base = "builtin:codex"

[[agent]]
name = "implementation-worker"
scope = "rig"
provider = "codex"
`)
	cfg, _, err := LoadWithIncludes(fsys.OSFS{}, filepath.Join(dir, "city.toml"))
	if err != nil {
		t.Fatalf("LoadWithIncludes: %v", err)
	}
	var worker *Agent
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "implementation-worker" {
			worker = &cfg.Agents[i]
			break
		}
	}
	if worker == nil {
		t.Fatal("implementation-worker agent not found in composed config")
	}
	if worker.IdleTimeout != DefaultPoolIdleTimeout {
		t.Errorf("IdleTimeout = %q, want %q — a pack worker declaring no lifecycle key must load with the safe default", worker.IdleTimeout, DefaultPoolIdleTimeout)
	}
}
