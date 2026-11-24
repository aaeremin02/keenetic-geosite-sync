package syncer

import (
	"context"
	"slices"
	"strings"
	"testing"

	"keenetic-geosite-sync/internal/config"
	"keenetic-geosite-sync/internal/keenetic"
	"keenetic-geosite-sync/internal/lists"
)

func TestBuildPlanRetainsRoutesAndOnlyRemovesRecognizedChunks(t *testing.T) {
	const facebook = "https://example.net/data/facebook"
	state := keenetic.ParseRunningConfig(`
object-group fqdn domain-list1
 description "Facebook 1"
 include old.example
!
object-group fqdn domain-list1-2
 description "Facebook 2"
 include oldpart.example
!
object-group fqdn domain-list1-3
 description "Facebook 3"
!
object-group fqdn domain-list1-4
 description "Different list"
!
dns-proxy
 route object-group domain-list1 Proxy0 reject
 route disable
 route object-group domain-list1-2 Proxy0 reject disabled
 route object-group domain-list1-3 Manual0 auto
!
`)
	cfg := config.Config{
		Prefix: "domain-list", RouteInterface: "Wireguard0", MaxEntriesPerGroup: 2,
		Lists: []config.List{{Name: "Facebook", Source: config.SourceV2Fly, URL: facebook}},
	}
	plan, err := BuildPlan(t.Context(), cfg, state, func(_ context.Context, source config.List) (lists.Result, error) {
		if source.URL != facebook {
			t.Fatalf("selected list %q, want facebook", source.URL)
		}
		return lists.Result{Entries: []string{"new.example"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Lists) != 1 || len(plan.Lists[0].Groups) != 1 {
		t.Fatalf("expected one base group: %+v", plan)
	}

	list := plan.Lists[0]
	group := list.Groups[0]
	if group.Name != "domain-list1" || group.Description != "Facebook" || group.Change != "update" {
		t.Fatalf("unexpected base group: %+v", group)
	}
	if len(group.Routes) != 2 ||
		!group.Routes[0].Exists || !group.Routes[0].Disabled || group.Routes[0].Interface != "Proxy0" ||
		!slices.Equal(group.Routes[0].Options, []string{"reject"}) ||
		group.Routes[1].Exists || group.Routes[1].Interface != "Wireguard0" {
		t.Fatalf("manual and configured routes not preserved in plan: %+v", group.Routes)
	}
	if (len(list.Stale) != 1 || list.Stale[0].Name != "domain-list1-2") || len(plan.Warnings) != 2 {
		t.Fatalf("unsafe cleanup plan: stale=%v, warnings=%v", list.Stale, plan.Warnings)
	}

	_, err = BuildPlan(t.Context(), cfg, state, func(context.Context, config.List) (lists.Result, error) {
		return lists.Result{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no usable entries") {
		t.Fatalf("empty source list must prevent an update, got %v", err)
	}
}

func TestSplitTitlesNumberTheFirstPartWithoutRenamingItsGroup(t *testing.T) {
	const youtube = "https://example.net/data/youtube"
	state := keenetic.ParseRunningConfig(`
object-group fqdn domain-list5
 description "YouTube"
 include a.example
!
object-group fqdn domain-list5-2
 description "YouTube 2"
 include b.example
!
dns-proxy
 route object-group domain-list5 Wireguard0 auto
 route object-group domain-list5-2 Wireguard0 auto
!
`)
	cfg := config.Config{
		Prefix: "domain-list", MaxEntriesPerGroup: 1,
		Lists: []config.List{{Name: "YouTube", Source: config.SourceV2Fly, URL: youtube}},
	}
	resolve := func(_ context.Context, source config.List) (lists.Result, error) {
		if source.URL != youtube {
			t.Fatalf("selected list %q, want youtube", source.URL)
		}
		return lists.Result{Entries: []string{"a.example", "b.example"}}, nil
	}

	plan, err := BuildPlan(t.Context(), cfg, state, resolve)
	if err != nil {
		t.Fatal(err)
	}
	first, second := plan.Lists[0].Groups[0], plan.Lists[0].Groups[1]
	if first.Name != "domain-list5" || first.Description != "YouTube 1" ||
		second.Name != "domain-list5-2" || second.Description != "YouTube 2" {
		t.Fatalf("unexpected split groups: %+v", plan.Lists[0].Groups)
	}
	commands, err := updateCommands(plan)
	if err != nil || !slices.Equal(commands, []string{`object-group fqdn domain-list5 description "YouTube 1"`}) {
		t.Fatalf("renumbering would touch more than the description: %q, %v", commands, err)
	}

	base := state.Groups[first.Name]
	base.Description = first.Description
	state.Groups[first.Name] = base
	delete(state.Groups, second.Name)
	delete(state.Routes, second.Name)
	cfg.MaxEntriesPerGroup = 2
	plan, err = BuildPlan(t.Context(), cfg, state, resolve)
	if err != nil {
		t.Fatal(err)
	}
	group := plan.Lists[0].Groups[0]
	if group.Name != first.Name || group.Description != "YouTube" {
		t.Fatalf("unsplit list failed to recover the original group: %+v", group)
	}
}

func TestExplicitSourcesReuseExistingGroupsWhenIPv6IsFiltered(t *testing.T) {
	const directURL = "https://example.net/cidr.txt"
	const geositeURL = "https://example.net/data/youtube"
	state := keenetic.ParseRunningConfig(`
object-group fqdn domain-list3
 description "Подсети Telegram"
 include 192.0.2.1/32
 include 2001:DB8::/32
!
object-group fqdn domain-list5
 description "Видеохостинг"
 include youtube.example
!
object-group fqdn domain-list6
 description "Manual list"
 include manual.example
!
dns-proxy route object-group domain-list3 Wireguard0 auto
`)
	cfg := config.Config{
		Prefix: "domain-list", RouteInterface: "Wireguard0", MaxEntriesPerGroup: 300,
		Lists: []config.List{
			{Name: "Подсети Telegram", Source: config.SourcePlain, URL: directURL},
			{Name: "Видеохостинг", Source: config.SourceV2Fly, URL: geositeURL},
		},
	}
	plan, err := BuildPlan(t.Context(), cfg, state, func(_ context.Context, source config.List) (lists.Result, error) {
		switch {
		case source.Source == config.SourcePlain && source.URL == directURL:
			return lists.Result{Entries: []string{"192.0.2.1"}, SkippedIPv6: 1}, nil
		case source.Source == config.SourceV2Fly && source.URL == geositeURL:
			return lists.Result{Entries: []string{"youtube.example"}}, nil
		default:
			t.Fatalf("unexpected source: %+v", source)
			return lists.Result{}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Lists) != 2 || plan.Lists[0].Groups[0].Name != "domain-list3" || plan.Lists[1].Groups[0].Name != "domain-list5" {
		t.Fatalf("lists should use configured sources and existing group names: %+v", plan.Lists)
	}

	commands, err := updateCommands(plan)
	want := []string{
		"no object-group fqdn domain-list3 include 2001:db8::/32",
		"dns-proxy route object-group domain-list5 Wireguard0 auto",
	}
	if err != nil || !slices.Equal(commands, want) {
		t.Fatalf("sync should remove IPv6 without replacing the equivalent IPv4 or overwriting a route: %q, %v", commands, err)
	}
}
