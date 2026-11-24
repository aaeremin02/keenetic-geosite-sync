package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"keenetic-geosite-sync/internal/keenetic"
	"keenetic-geosite-sync/internal/syncer"
)

func TestValidateWorksWithoutNDMC(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"lists":[{"source":"v2fly","lists":["youtube"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"validate", "--config", path}, &out, &stderr); code != 0 ||
		!strings.Contains(out.String(), "Configuration valid") {
		t.Fatalf("validate failed with code %d, stdout %q, stderr %q", code, &out, &stderr)
	}
}

func TestPrintPlanShowsRouteChangeForUnchangedGroup(t *testing.T) {
	groups := []syncer.GroupPlan{
		{Name: "domain-list0", Description: "Discord", Change: "keep"},
		{Name: "domain-list1", Description: "Telegram", Change: "keep", Domains: []string{"192.0.2.1"}},
	}
	route := keenetic.Route{Interface: "Wireguard0", Options: []string{"auto"}}
	groups[0].Routes = []syncer.RoutePlan{{Route: route, Exists: true}}
	groups[1].Routes = []syncer.RoutePlan{{Route: route}}
	plan := syncer.Plan{Lists: []syncer.ListPlan{{Groups: groups}}}

	var out bytes.Buffer
	printPlan(&out, plan, false)
	want := "  update domain-list1 (\"Telegram\", 1 entries)\n    add route Wireguard0 auto\n"
	if out.String() != want {
		t.Fatalf("apply plan output = %q, want %q", &out, want)
	}

	groups[1].Routes[0].Exists = true
	out.Reset()
	printPlan(&out, plan, true)
	if want := "Dry run\nNo changes\n"; out.String() != want {
		t.Fatalf("dry run output = %q, want %q", &out, want)
	}
}

func TestPendingSaveRetriesWithoutReapplyingChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save-state")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()

	calls := 0
	failing := true
	save := configurationSaver{lock, func(_ context.Context, command string) error {
		if command != saveCommand {
			t.Fatalf("unexpected router command %q", command)
		}
		calls++
		if failing {
			return errors.New("save refused")
		}
		return nil
	}}

	verified := keenetic.ParseRunningConfig("! first header\nhostname Keenetic\n").Fingerprint
	var out, stderr bytes.Buffer
	result := syncer.ApplyResult{Commands: 2, Fingerprint: verified}
	if code := finishApply(t.Context(), &save, &out, &stderr, result, nil); code != 1 ||
		calls != saveAttempts || !strings.Contains(stderr.String(), "save failed after 3 attempts") ||
		strings.Contains(out.String(), "Configuration saved") {
		t.Fatalf("failed save reported code %d, calls %d, stdout %q, stderr %q", code, calls, &out, &stderr)
	}

	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	// A subsequent process opens the same marker and saves the verified configuration.
	lock, err = os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	save.lock = lock
	failing = false
	current := keenetic.ParseRunningConfig("! later header\r\nhostname Keenetic\r\n").Fingerprint
	if message, err := save.resume(t.Context(), current); err != nil ||
		message != "Pending configuration saved" || calls != saveAttempts+1 {
		t.Fatalf("next run did not recover the save: %q, %v, calls %d", message, err, calls)
	}
	if message, err := save.resume(t.Context(), current); err != nil || message != "" || calls != saveAttempts+1 {
		t.Fatalf("idle run saved again: %q, %v, calls %d", message, err, calls)
	}

	if _, err := lock.WriteAt(verified[:], 0); err != nil {
		t.Fatal(err)
	}
	changed := keenetic.ParseRunningConfig("hostname Edited\n").Fingerprint
	if message, err := save.resume(t.Context(), changed); err != nil ||
		!strings.Contains(message, "no longer matches") || calls != saveAttempts+1 {
		t.Fatalf("retry should leave a manually changed configuration unsaved: %q, %v, calls %d", message, err, calls)
	}
}
