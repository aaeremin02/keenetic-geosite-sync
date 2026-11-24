package syncer

import (
	"context"
	"errors"
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
	const save = "system configuration save"
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
		{"verified update", fakeRouter{reply: []keenetic.State{updated, final}}, []string{add, remove, deleteChunk, save}, ""},
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
	if calls, err := ApplySync(t.Context(), repeat, &fakeRouter{}); err != nil || calls != 0 {
		t.Fatalf("repeat sync changed configuration or failed: %d, %v", calls, err)
	}

	initial.Groups["kgs9"] = keenetic.Group{Description: "Facebook"}
	cleanup, err := BuildCleanup(cfg, initial)
	if err != nil || len(cleanup.Groups) != 2 || cleanup.Groups[0].Name != oldChunk || cleanup.Groups[1].Name != base {
		t.Fatalf("cleanup did not honor the configured list scope: %+v, %v", cleanup, err)
	}
	router := fakeRouter{reply: []keenetic.State{initial, {
		Groups: map[string]keenetic.Group{"kgs9": initial.Groups["kgs9"]},
	}}}
	if count, err := ApplyCleanup(t.Context(), cleanup, &router); err != nil || count != 2 ||
		!slices.Equal(router.commands, []string{deleteChunk, "no object-group fqdn kgs0", save}) {
		t.Fatalf("cleanup did not verify and save the scoped deletion: %q, %d, %v", router.commands, count, err)
	}
}
