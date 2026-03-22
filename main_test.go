package main

import (
	"testing"
	"time"

	"github.com/stefanoguerrini/c9s/internal/claude"
	"github.com/stefanoguerrini/c9s/internal/tmux"
)

func TestReconcileWindows_NewSession(t *testing.T) {
	// New session tracked with tmpKey should be reconciled to real sessionID.
	m := &model{
		managedWindows: map[string]managedWindow{
			"new-123456": {
				windowID:   "@1",
				sessionID:  "",
				project:    "/home/user/project",
				paneStatus: tmux.PaneWaiting,
			},
		},
	}

	sessions := []claude.SessionInfo{
		{
			SessionID:   "abc-def-123",
			ProjectPath: "/home/user/project",
			FileMtime:   time.Now(),
		},
	}
	procs := []claude.ClaudeProcess{
		{PID: 100, SessionID: "", ProjectPath: "/home/user/project"},
	}

	getPanePID := func(windowID string) (int, error) { return 50, nil }
	getChildPIDs := func(pid int) []int { return []int{100} }

	m.reconcileWindows(sessions, procs, getPanePID, getChildPIDs)

	// Should be re-keyed from "new-123456" to "abc-def-123".
	if _, ok := m.managedWindows["new-123456"]; ok {
		t.Error("old tmpKey should be deleted")
	}
	mw, ok := m.managedWindows["abc-def-123"]
	if !ok {
		t.Fatal("expected entry under real sessionID")
	}
	if mw.windowID != "@1" {
		t.Errorf("windowID = %q, want @1", mw.windowID)
	}
	if mw.sessionID != "abc-def-123" {
		t.Errorf("sessionID = %q, want abc-def-123", mw.sessionID)
	}
}

func TestReconcileWindows_Fork(t *testing.T) {
	// After fork: old sessionID maps to window. Claude process still has
	// --resume old-session-id in args, but old session JSONL is stale and
	// new forked session JSONL is recently active.
	m := &model{
		managedWindows: map[string]managedWindow{
			"old-session-id": {
				windowID:   "@2",
				sessionID:  "old-session-id",
				project:    "/home/user/project",
				paneStatus: tmux.PaneProcessing,
			},
		},
	}

	sessions := []claude.SessionInfo{
		{
			SessionID:   "old-session-id",
			ProjectPath: "/home/user/project",
			FileMtime:   time.Now().Add(-5 * time.Minute), // stale
		},
		{
			SessionID:   "forked-session-id",
			ProjectPath: "/home/user/project",
			FileMtime:   time.Now(), // recently active
		},
	}
	// Process still shows --resume old-session-id (realistic fork behavior).
	procs := []claude.ClaudeProcess{
		{PID: 200, SessionID: "old-session-id", ProjectPath: "/home/user/project"},
	}

	getPanePID := func(windowID string) (int, error) { return 60, nil }
	getChildPIDs := func(pid int) []int { return []int{200} }

	m.reconcileWindows(sessions, procs, getPanePID, getChildPIDs)

	// Should be re-keyed from "old-session-id" to "forked-session-id".
	if _, ok := m.managedWindows["old-session-id"]; ok {
		t.Error("old sessionID key should be deleted")
	}
	mw, ok := m.managedWindows["forked-session-id"]
	if !ok {
		t.Fatal("expected entry under forked sessionID")
	}
	if mw.sessionID != "forked-session-id" {
		t.Errorf("sessionID = %q, want forked-session-id", mw.sessionID)
	}
}

func TestReconcileWindows_ResumeMatch(t *testing.T) {
	// Process has --resume flag pointing to an active session (not stale).
	// tmpKey entry should be reconciled to that session.
	m := &model{
		managedWindows: map[string]managedWindow{
			"new-999": {
				windowID: "@3",
				project:  "/home/user/project",
			},
		},
	}

	sessions := []claude.SessionInfo{
		{
			SessionID:   "resumed-id",
			ProjectPath: "/home/user/project",
			FileMtime:   time.Now(),
		},
	}
	procs := []claude.ClaudeProcess{
		{PID: 300, SessionID: "resumed-id", ProjectPath: "/home/user/project"},
	}

	getPanePID := func(windowID string) (int, error) { return 70, nil }
	getChildPIDs := func(pid int) []int { return []int{300} }

	m.reconcileWindows(sessions, procs, getPanePID, getChildPIDs)

	if _, ok := m.managedWindows["new-999"]; ok {
		t.Error("tmpKey should be deleted")
	}
	if _, ok := m.managedWindows["resumed-id"]; !ok {
		t.Error("expected entry under resumed sessionID")
	}
}

func TestReconcileWindows_ActiveSessionSkipped(t *testing.T) {
	// If current sessionID is valid and recently active, don't reconcile.
	m := &model{
		managedWindows: map[string]managedWindow{
			"active-id": {
				windowID:  "@4",
				sessionID: "active-id",
				project:   "/home/user/project",
			},
		},
	}

	sessions := []claude.SessionInfo{
		{
			SessionID:   "active-id",
			ProjectPath: "/home/user/project",
			FileMtime:   time.Now(), // recently active
		},
	}

	callCount := 0
	getPanePID := func(windowID string) (int, error) {
		callCount++
		return 80, nil
	}
	getChildPIDs := func(pid int) []int { return nil }

	m.reconcileWindows(sessions, nilProcs(), getPanePID, getChildPIDs)

	// GetPanePID should NOT be called since active session is skipped.
	if callCount != 0 {
		t.Errorf("getPanePID called %d times, expected 0 (should skip active sessions)", callCount)
	}
	if _, ok := m.managedWindows["active-id"]; !ok {
		t.Error("active session should remain in map")
	}
}

func TestReconcileWindows_AmbiguousProject(t *testing.T) {
	// Multiple active sessions in same project — should pick the most recent.
	m := &model{
		managedWindows: map[string]managedWindow{
			"old-id": {
				windowID:  "@5",
				sessionID: "old-id",
				project:   "/home/user/project",
			},
		},
	}

	now := time.Now()
	sessions := []claude.SessionInfo{
		{
			SessionID:   "old-id",
			ProjectPath: "/home/user/project",
			FileMtime:   now.Add(-5 * time.Minute), // stale
		},
		{
			SessionID:   "session-a",
			ProjectPath: "/home/user/project",
			FileMtime:   now.Add(-10 * time.Second), // recent
		},
		{
			SessionID:   "session-b",
			ProjectPath: "/home/user/project",
			FileMtime:   now.Add(-2 * time.Second), // most recent
		},
	}
	procs := []claude.ClaudeProcess{
		{PID: 400, SessionID: "old-id", ProjectPath: "/home/user/project"},
	}

	getPanePID := func(windowID string) (int, error) { return 90, nil }
	getChildPIDs := func(pid int) []int { return []int{400} }

	m.reconcileWindows(sessions, procs, getPanePID, getChildPIDs)

	// Should pick the most recently active session.
	if _, ok := m.managedWindows["old-id"]; ok {
		t.Error("old entry should be deleted")
	}
	if _, ok := m.managedWindows["session-b"]; !ok {
		t.Error("expected entry under most recent session-b")
	}
}

func TestReconcileWindows_NoChildPIDs(t *testing.T) {
	// If no child PIDs found (process exited), entry should remain unchanged.
	m := &model{
		managedWindows: map[string]managedWindow{
			"stale-id": {
				windowID:  "@6",
				sessionID: "stale-id",
				project:   "/home/user/project",
			},
		},
	}

	sessions := []claude.SessionInfo{
		{
			SessionID:   "stale-id",
			ProjectPath: "/home/user/project",
			FileMtime:   time.Now().Add(-5 * time.Minute),
		},
	}

	getPanePID := func(windowID string) (int, error) { return 100, nil }
	getChildPIDs := func(pid int) []int { return nil } // no children

	m.reconcileWindows(sessions, nil, getPanePID, getChildPIDs)

	if _, ok := m.managedWindows["stale-id"]; !ok {
		t.Error("entry should remain when no child PIDs found")
	}
}

func nilProcs() []claude.ClaudeProcess {
	return nil
}
