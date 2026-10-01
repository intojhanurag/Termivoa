package protocol

import (
	"slices"
	"strings"
	"testing"
)

func TestWorktreeName(t *testing.T) {
	for _, name := range []string{"a", "brave-otter", "x1-2-y", strings.Repeat("a", 40)} {
		if !ValidWorktreeName(name) {
			t.Errorf("%q was refused", name)
		}
	}
	for _, name := range []string{"", "-a", "a-", "a--b", "Brave", "a_b", "a.b", "a/b", "..", "é", strings.Repeat("a", 41)} {
		if ValidWorktreeName(name) {
			t.Errorf("%q was accepted", name)
		}
	}
}

func TestWorktreeCreateValidation(t *testing.T) {
	good := WorktreeCreate{SourceSessionID: "22222222-2222-4222-8222-222222222222", Name: "brave-otter", Base: "origin/main"}
	if err := ValidateWorktreeCreate(&good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*WorktreeCreate){
		"bad source": func(w *WorktreeCreate) { w.SourceSessionID = "x" },
		"bad name":   func(w *WorktreeCreate) { w.Name = "../x" },
		"empty base": func(w *WorktreeCreate) { w.Base = "" },
		"long base":  func(w *WorktreeCreate) { w.Base = strings.Repeat("a", 257) },
		"nul base":   func(w *WorktreeCreate) { w.Base = "a\x00" },
	} {
		bad := good
		change(&bad)
		if ValidateWorktreeCreate(&bad) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestBranchesAndWorktreeMessages(t *testing.T) {
	for _, message := range []struct {
		kind    MessageType
		payload any
	}{{BranchesList, BranchesListPayload{}}, {WorktreeRemove, WorktreeRemovePayload{Lease: strings.Repeat("a", 43), Force: true}}} {
		if _, err := DecodeClient(encodeTestMessage(t, message.kind, message.payload, true)); err != nil {
			t.Errorf("%s: %v", message.kind, err)
		}
		if _, err := DecodeClient(encodeTestMessage(t, message.kind, message.payload, false)); err == nil {
			t.Errorf("%s without session identity was accepted", message.kind)
		}
		if _, err := DecodeHost(encodeTestMessage(t, message.kind, message.payload, true)); err == nil {
			t.Errorf("host sent %s", message.kind)
		}
	}
	if _, err := DecodeClient(encodeTestMessage(t, WorktreeRemove, WorktreeRemovePayload{Lease: "short"}, true)); err == nil {
		t.Error("worktree.remove with a bad lease was accepted")
	}

	ready := BranchesListedPayload{
		State: ChangesStateReady, Repo: stringPointer("my repo"), DefaultBranch: stringPointer("main"), Current: stringPointer("feat/x"),
		Branches: []BranchRef{{Name: "main"}, {Name: "origin/main", Remote: true}},
	}
	if _, err := DecodeHost(encodeTestMessage(t, BranchesListed, ready, true)); err != nil {
		t.Fatalf("valid branches.listed: %v", err)
	}
	if _, err := DecodeClient(encodeTestMessage(t, BranchesListed, ready, true)); err == nil {
		t.Fatal("client sent branches.listed")
	}
	for _, state := range []ChangesState{NotGit, ChangesStateUnavailable} {
		if _, err := DecodeHost(encodeTestMessage(t, BranchesListed, BranchesListedPayload{State: state, Branches: []BranchRef{}}, true)); err != nil {
			t.Errorf("%s: %v", state, err)
		}
	}
	for name, payload := range map[string]BranchesListedPayload{
		"bad state":      {State: "done", Branches: []BranchRef{}},
		"nil branches":   {State: ChangesStateReady},
		"too many":       {State: ChangesStateReady, Branches: slices.Repeat([]BranchRef{{Name: "a"}}, MaxBranches+1)},
		"branch not git": {State: NotGit, Branches: []BranchRef{{Name: "a"}}},
		"repo not git":   {State: NotGit, Repo: stringPointer("a"), Branches: []BranchRef{}},
		"control repo":   {State: ChangesStateReady, Repo: stringPointer("a\nb"), Branches: []BranchRef{}},
		"long repo":      {State: ChangesStateReady, Repo: stringPointer(strings.Repeat("a", 129)), Branches: []BranchRef{}},
		"empty branch":   {State: ChangesStateReady, Branches: []BranchRef{{Name: ""}}},
		"long branch":    {State: ChangesStateReady, Branches: []BranchRef{{Name: strings.Repeat("a", 257)}}},
		"control branch": {State: ChangesStateReady, Branches: []BranchRef{{Name: "a\x1b[2J"}}},
		"empty default":  {State: ChangesStateReady, DefaultBranch: stringPointer(""), Branches: []BranchRef{}},
	} {
		if _, err := DecodeHost(encodeTestMessage(t, BranchesListed, payload, true)); err == nil {
			t.Errorf("%s branches.listed was accepted", name)
		}
	}

	for _, payload := range []WorktreeRemovedPayload{{State: Removed}, {State: Dirty}, {State: WorktreeRemoveStateFailed, Output: stringPointer("error")}} {
		if _, err := DecodeHost(encodeTestMessage(t, WorktreeRemoved, payload, true)); err != nil {
			t.Errorf("%s: %v", payload.State, err)
		}
	}
	for _, payload := range []WorktreeRemovedPayload{{State: "gone"}, {State: WorktreeRemoveStateFailed, Output: stringPointer(strings.Repeat("a", MaxGitOutput+1))}} {
		if _, err := DecodeHost(encodeTestMessage(t, WorktreeRemoved, payload, true)); err == nil {
			t.Errorf("worktree.removed %+v was accepted", payload)
		}
	}
}
