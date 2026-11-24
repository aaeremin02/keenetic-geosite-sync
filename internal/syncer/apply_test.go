package syncer

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"keenetic-geosite-sync/internal/config"
	"keenetic-geosite-sync/internal/keenetic"
	"keenetic-geosite-sync/internal/lists"
)

type fakeRouter struct {
	commands []string
	reply    []keenetic.State
	fail     string
}

func (r *fakeRouter) ReadRunningConfig(context.Context) (keenetic.State, error) {
	if len(r.reply) == 0 {
		return keenetic.State{}, errors.New("unexpected running configuration read")
	}
	state := r.reply[0]
	r.reply = r.reply[1:]
	return state, nil
}

func (r *fakeRouter) RunCommand(_ context.Context, command string) error {
	r.commands = append(r.commands, command)
	if command == r.fail {
		return errors.New("ndmc refused the change")
	}
	return nil
}

func TestApplySyncChecksMembershipBeforeDeletingStaleGroups(t *testing.T) {
	const base = "kgs0"
	const oldChunk = "kgs0-2"
	initial := keenetic.ParseRunningConfig(`
object-group fqdn kgs0
 description "YouTube"
 include old.example
!
object-group fqdn kgs0-2
 description "YouTube 2"
 include earlier.example
!
dns-proxy
 route object-group kgs0 Wireguard0 auto reject
 route object-group kgs0-2 Wireguard0 auto reject
!
`)
	resolve := func(context.Context, config.List) (lists.Result, error) {
		return lists.Result{Entries: []string{"new.example"}}, nil
	}
	cfg := config.Config{
		Prefix: "kgs", MaxEntriesPerGroup: 2,
		Lists: []config.List{{Name: "YouTube", Source: config.SourceV2Fly, URL: "https://example.net/youtube"}},
	}
	plan, err := BuildPlan(t.Context(), cfg, initial, resolve)
	if err != nil {
		t.Fatal(err)
	}

	const add = "object-group fqdn kgs0 include new.example"
	const remove = "no object-group fqdn kgs0 include old.example"
	const deleteChunk = "no object-group fqdn kgs0-2"
	updated := keenetic.ParseRunningConfig(`
object-group fqdn kgs0
 description "YouTube"
 include new.example
!
object-group fqdn kgs0-2
 description "YouTube 2"
 include earlier.example
!
dns-proxy
 route object-group kgs0 Wireguard0 auto reject
 route object-group kgs0-2 Wireguard0 auto reject
!
`)
	final := keenetic.State{
		Groups: map[string]keenetic.Group{base: updated.Groups[base]},
		Routes: map[string][]keenetic.Route{base: updated.Routes[base]},
	}

	for _, tc := range []struct {
		name   string
		router fakeRouter
		want   []string
		err    string
	}{
		{"ndmc error", fakeRouter{fail: add}, []string{add}, "refused"},
		{"silent rejection", fakeRouter{reply: []keenetic.State{initial}}, []string{add, remove}, "did not apply"},
		{"verified update", fakeRouter{reply: []keenetic.State{updated, final}}, []string{add, remove, deleteChunk}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplySync(t.Context(), plan, &tc.router)
			if (tc.err == "" && err != nil) || (tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err))) {
				t.Fatalf("apply error = %v, expected %q", err, tc.err)
			}
			if !slices.Equal(tc.router.commands, tc.want) {
				t.Fatalf("commands = %q, wanted %q", tc.router.commands, tc.want)
			}
		})
	}

	repeat, err := BuildPlan(t.Context(), cfg, final, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := ApplySync(t.Context(), repeat, &fakeRouter{}); err != nil || result.Commands != 0 {
		t.Fatalf("repeat sync changed configuration or failed: %d, %v", result.Commands, err)
	}

	initial.Groups["kgs9"] = keenetic.Group{Description: "Facebook"}
	cleanup, err := BuildCleanup(cfg, initial)
	if err != nil || len(cleanup.Groups) != 2 || cleanup.Groups[0].Name != oldChunk || cleanup.Groups[1].Name != base {
		t.Fatalf("cleanup did not honor the configured list scope: %+v, %v", cleanup, err)
	}
	router := fakeRouter{reply: []keenetic.State{initial, {
		Groups: map[string]keenetic.Group{"kgs9": initial.Groups["kgs9"]},
	}}}
	if result, err := ApplyCleanup(t.Context(), cleanup, &router); err != nil || result.Commands != 2 ||
		!slices.Equal(router.commands, []string{deleteChunk, "no object-group fqdn kgs0"}) {
		t.Fatalf("cleanup did not verify the scoped deletion: %q, %d, %v", router.commands, result.Commands, err)
	}
}

func TestApplySyncPrunesRemovedListsWithinDedicatedPrefix(t *testing.T) {
	state := keenetic.ParseRunningConfig(`
object-group fqdn kgs
 description "Outside prefix"
!
object-group fqdn kgs-4
 description "Active"
 include active.example
!
object-group fqdn kgs-1
 description "Removed 1"
 include old.example
!
object-group fqdn kgs-1-2
 description "Removed 2"
 include old-part.example
!
object-group fqdn kgs-1-3
 description "Removed 3"
 include manual-part.example
!
object-group fqdn kgs-2
 description "With exclusions"
 exclude keep.example
!
object-group fqdn kgs-3-2
 description "Orphan 2"
!
object-group fqdn kgs-manual
 description "Manual"
!
object-group fqdn domain-list0
 description "Web UI"
!
object-group fqdn domain-name-list-0
 description "New Web UI"
!
dns-proxy
 route object-group kgs-1 Wireguard0 auto
 route object-group kgs-1-2 Wireguard0 auto
 route object-group kgs-1-3 Manual0 auto
!
`)
	cfg := config.Config{Prefix: "kgs-", MaxEntriesPerGroup: 300, Lists: []config.List{{Name: "Active"}}}
	resolve := func(context.Context, config.List) (lists.Result, error) {
		return lists.Result{Entries: []string{"active.example"}}, nil
	}
	plan, err := BuildPlan(t.Context(), cfg, state, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pruned) != 2 || plan.Pruned[0].Name != "kgs-1-2" || plan.Pruned[1].Name != "kgs-1" || len(plan.Warnings) != 2 {
		t.Fatalf("unexpected removal plan: %+v", plan)
	}

	edited := state
	edited.Routes = maps.Clone(state.Routes)
	edited.Routes["kgs-1"] = []keenetic.Route{{Interface: "Manual0"}}
	router := fakeRouter{reply: []keenetic.State{edited}}
	if _, err := ApplySync(t.Context(), plan, &router); err == nil || len(router.commands) != 0 {
		t.Fatalf("a route changed after planning, commands = %q, error = %v", router.commands, err)
	}

	final := state
	final.Groups, final.Routes = maps.Clone(state.Groups), maps.Clone(state.Routes)
	for _, prune := range plan.Pruned {
		delete(final.Groups, prune.Name)
		delete(final.Routes, prune.Name)
	}
	router = fakeRouter{reply: []keenetic.State{state, final}}
	result, err := ApplySync(t.Context(), plan, &router)
	want := []string{"no object-group fqdn kgs-1-2", "no object-group fqdn kgs-1"}
	if err != nil || result.Commands != len(want) || !slices.Equal(router.commands, want) {
		t.Fatalf("commands = %q, wanted %q, error = %v", router.commands, want, err)
	}

	cfg.Lists = nil
	empty, err := BuildPlan(t.Context(), cfg, final, nil)
	if err != nil || len(empty.Pruned) != 1 || empty.Pruned[0].Name != "kgs-4" {
		t.Fatalf("removing the last list should prune its group: %+v, %v", empty, err)
	}
	for _, legacy := range []string{"domain-list", "domain-name-list-"} {
		cfg.Prefix = legacy
		plan, err := BuildPlan(t.Context(), cfg, state, nil)
		if err != nil || len(plan.Pruned) != 0 {
			t.Fatalf("built-in prefix %q should protect Web UI groups: %+v, %v", legacy, plan, err)
		}
	}
}
