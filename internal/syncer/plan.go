package syncer

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"keenetic-geosite-sync/internal/config"
	"keenetic-geosite-sync/internal/keenetic"
	"keenetic-geosite-sync/internal/lists"
)

type Plan struct {
	Lists    []ListPlan
	Warnings []string
}

type ListPlan struct {
	Name           string
	Groups         []GroupPlan
	Stale          []Removal
	SkippedKeyword int
	SkippedRegexp  int
	SkippedIPv6    int
}

type GroupPlan struct {
	Name, Description string
	Domains           []string
	Change            string
	Routes            []RoutePlan
	add               []string
	remove            []string
	makeRoom          int
	setDescription    bool
}

func (group GroupPlan) DomainChanges() (added, removed int) {
	return len(group.add), len(group.remove)
}

type RoutePlan struct {
	keenetic.Route
	Exists bool
}

type target struct {
	name, title string
	source      config.List
}

var (
	numberedName  = regexp.MustCompile(`^(.+)-([2-9]|[1-9][0-9]+)$`)
	legacyNumber  = regexp.MustCompile(`\s+-?([1-9][0-9]*)$`)
	bracketedPart = regexp.MustCompile(`\s*\[([1-9][0-9]*)/[1-9][0-9]*\]$`)
)

func BuildPlan(ctx context.Context, cfg config.Config, state keenetic.State,
	resolve func(context.Context, config.List) (lists.Result, error),
) (Plan, error) {
	targets, err := selectTargets(cfg, state)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	for _, target := range targets {
		result, err := resolve(ctx, target.source)
		if err != nil {
			return Plan{}, fmt.Errorf("resolve %q for %s: %w", target.title, target.name, err)
		}
		if len(result.Entries) == 0 {
			return Plan{}, fmt.Errorf("%q has no usable entries; keeping the existing configuration (%d keyword, %d regexp, %d IPv6 skipped)",
				target.title, result.SkippedKeyword, result.SkippedRegexp, result.SkippedIPv6)
		}
		list, warnings, err := planList(cfg, state, target, result)
		if err != nil {
			return Plan{}, err
		}
		plan.Lists = append(plan.Lists, list)
		plan.Warnings = append(plan.Warnings, warnings...)
	}
	return plan, nil
}

func selectTargets(cfg config.Config, state keenetic.State) ([]target, error) {
	if len(cfg.Lists) == 0 {
		return nil, nil
	}
	requested := make(map[string]config.List, len(cfg.Lists))
	for _, source := range cfg.Lists {
		requested[titleKey(source.Name)] = source
	}

	existing := make(map[string]string)
	usedNames := make(map[string]bool)
	for _, name := range sortedNames(state.Groups) {
		usedNames[name] = true
		if !strings.HasPrefix(name, cfg.Prefix) {
			continue
		}
		if part := numberedName.FindStringSubmatch(name); part != nil {
			usedNames[part[1]] = true
			if _, parent := state.Groups[part[1]]; parent {
				continue
			}
			if _, err := strconv.Atoi(strings.TrimPrefix(part[1], cfg.Prefix)); err == nil {
				continue
			}
		}
		title := state.Groups[name].Description
		if base, part := splitDescription(title); part == 1 {
			child, hasChild := state.Groups[name+"-2"]
			_, wantsBase := requestedKey(requested, base)
			_, wantsTitle := requestedKey(requested, title)
			if (hasChild && isPart(child, base, 2)) || bracketedPart.MatchString(title) ||
				(!hasChild && wantsBase && !wantsTitle) {
				title = base
			}
		}
		id, selected := requestedKey(requested, title)
		if !selected {
			continue
		}
		if previous, exists := existing[id]; exists {
			return nil, fmt.Errorf("groups %s and %s both match list %q", previous, name, title)
		}
		existing[id] = name
	}

	targets := make([]target, 0, len(cfg.Lists))
	next := 0
	for _, source := range cfg.Lists {
		name := existing[titleKey(source.Name)]
		if name == "" {
			for usedNames[cfg.Prefix+strconv.Itoa(next)] {
				next++
			}
			name = cfg.Prefix + strconv.Itoa(next)
			usedNames[name] = true
		}
		targets = append(targets, target{name: name, title: source.Name, source: source})
	}
	return targets, nil
}

func titleKey(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

func v2flyTitleKey(title string) string {
	return strings.Join(strings.Fields(titleKey(title)), "-")
}

func requestedKey(requested map[string]config.List, title string) (string, bool) {
	key := titleKey(title)
	if _, exists := requested[key]; exists {
		return key, true
	}
	alias := v2flyTitleKey(title)
	return alias, requested[alias].Source == config.SourceV2Fly
}

func planList(cfg config.Config, state keenetic.State, item target, result lists.Result) (ListPlan, []string, error) {
	list := ListPlan{
		Name: item.title, SkippedKeyword: result.SkippedKeyword, SkippedRegexp: result.SkippedRegexp, SkippedIPv6: result.SkippedIPv6,
	}
	baseRoutes := state.Routes[item.name]
	chunks := (len(result.Entries)-1)/cfg.MaxEntriesPerGroup + 1

	for chunk := 1; chunk <= chunks; chunk++ {
		name, title := item.name, item.title
		if chunk > 1 {
			name += "-" + strconv.Itoa(chunk)
		}
		if chunks > 1 {
			title += " " + strconv.Itoa(chunk)
		}
		start := (chunk - 1) * cfg.MaxEntriesPerGroup
		size := min(cfg.MaxEntriesPerGroup, len(result.Entries)-start)
		domains := result.Entries[start : start+size]
		existing, exists := state.Groups[name]
		if exists && chunk > 1 && !item.matchesPart(existing, chunk) {
			return ListPlan{}, nil, fmt.Errorf("group %s already exists with unrelated description %q", name, existing.Description)
		}

		change := "create"
		if exists {
			change = "update"
			if existing.Description == title && sameDomains(existing.Domains, domains) {
				change = "keep"
			}
		}
		add, remove := domainChanges(existing.Domains, domains)
		room := min(len(remove), max(0, len(existing.Domains)+len(add)-cfg.MaxEntriesPerGroup))
		list.Groups = append(list.Groups, GroupPlan{
			Name: name, Description: title, Domains: domains, Change: change,
			Routes: plannedRoutes(state.Routes[name], baseRoutes, cfg.RouteInterface),
			add:    add, remove: remove, makeRoom: room,
			setDescription: !exists || existing.Description != title,
		})
	}

	var warnings []string
	for _, name := range sortedNames(state.Groups) {
		part := numberedName.FindStringSubmatch(name)
		if part == nil || part[1] != item.name {
			continue
		}
		number, err := strconv.Atoi(part[2])
		if err != nil || number <= chunks {
			continue
		}
		if !item.matchesPart(state.Groups[name], number) || state.Groups[name].HasExcludes ||
			hasIndependentRoute(state.Routes[name], baseRoutes) {
			warnings = append(warnings, fmt.Sprintf("keep %s: unrelated description, exclusions or independent route", name))
			continue
		}
		list.Stale = append(list.Stale, removal(state, name))
	}
	return list, warnings, nil
}

func plannedRoutes(existing, inherited []keenetic.Route, iface string) []RoutePlan {
	routes := make([]RoutePlan, 0, len(existing)+1)
	seenRoutes := make(map[[2]string]bool)
	seenInterfaces := make(map[string]bool)
	for _, route := range existing {
		routes = append(routes, RoutePlan{route, true})
		seenRoutes[routeKey(route)] = true
		seenInterfaces[route.Interface] = true
	}
	for _, route := range inherited {
		key := routeKey(route)
		if !seenRoutes[key] {
			routes = append(routes, RoutePlan{route, false})
			seenRoutes[key] = true
			seenInterfaces[route.Interface] = true
		}
	}
	if iface != "" && !seenInterfaces[iface] {
		routes = append(routes, RoutePlan{keenetic.Route{Interface: iface, Options: []string{"auto"}}, false})
	}
	return routes
}

func routeKey(route keenetic.Route) [2]string {
	if route.Interface != "" {
		return [2]string{route.Interface, ""}
	}
	return [2]string{"", route.Gateway}
}

func hasIndependentRoute(routes, base []keenetic.Route) bool {
	for _, route := range routes {
		if !slices.ContainsFunc(base, route.Equal) {
			return true
		}
	}
	return false
}

func domainChanges(existing, desired []string) (add, remove []string) {
	current, next := make(map[string]string, len(existing)), make(map[string]bool, len(desired))
	for _, value := range existing {
		current[entryKey(value)] = value
	}
	for _, value := range desired {
		key := entryKey(value)
		next[key] = true
		if _, exists := current[key]; !exists {
			add = append(add, value)
		}
	}
	for key, value := range current {
		if !next[key] {
			remove = append(remove, value)
		}
	}
	sort.Strings(remove)
	return add, remove
}

func sameDomains(existing, desired []string) bool {
	return slices.Equal(sortedDomains(existing), sortedDomains(desired))
}

func sortedDomains(domains []string) []string {
	sorted := make([]string, 0, len(domains))
	for _, value := range domains {
		sorted = append(sorted, entryKey(value))
	}
	sort.Strings(sorted)
	return slices.Compact(sorted)
}

func entryKey(value string) string {
	if ip, _, ok := lists.NormalizeIP(value); ok {
		return ip
	}
	return strings.ToLower(value)
}

func sortedNames(groups map[string]keenetic.Group) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func splitDescription(description string) (string, int) {
	for _, pattern := range []*regexp.Regexp{bracketedPart, legacyNumber} {
		if match := pattern.FindStringSubmatch(description); match != nil {
			if number, err := strconv.Atoi(match[1]); err == nil {
				return strings.TrimSpace(strings.TrimSuffix(description, match[0])), number
			}
		}
	}
	return description, 0
}

func isPart(group keenetic.Group, title string, part int) bool {
	base, number := splitDescription(group.Description)
	return number == part && strings.EqualFold(base, title)
}

func (item target) matchesPart(group keenetic.Group, part int) bool {
	title, number := splitDescription(group.Description)
	if item.source.Source == config.SourceV2Fly {
		return number == part && v2flyTitleKey(title) == v2flyTitleKey(item.title)
	}
	return number == part && strings.EqualFold(title, item.title)
}
