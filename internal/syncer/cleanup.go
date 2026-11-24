package syncer

import (
	"fmt"
	"slices"
	"strconv"

	"keenetic-geosite-sync/internal/config"
	"keenetic-geosite-sync/internal/keenetic"
)

type CleanupPlan struct {
	Groups   []Removal
	Warnings []string
}

type Removal struct {
	Name   string
	group  keenetic.Group
	routes []keenetic.Route
}

func (r Removal) Description() string {
	return r.group.Description
}

func BuildCleanup(cfg config.Config, state keenetic.State) (CleanupPlan, error) {
	targets, err := selectTargets(cfg, state)
	if err != nil {
		return CleanupPlan{}, err
	}

	var plan CleanupPlan
	for _, target := range targets {
		group, exists := state.Groups[target.name]
		if !exists {
			continue
		}
		if group.HasExcludes {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("keep %s: group has exclusions", target.name))
			continue
		}

		for _, name := range sortedNames(state.Groups) {
			part := numberedName.FindStringSubmatch(name)
			if part == nil || part[1] != target.name {
				continue
			}
			number, err := strconv.Atoi(part[2])
			if err != nil || !target.matchesPart(state.Groups[name], number) ||
				state.Groups[name].HasExcludes || hasIndependentRoute(state.Routes[name], state.Routes[target.name]) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("keep %s: unrelated description, exclusions or independent route", name))
				continue
			}
			plan.Groups = append(plan.Groups, removal(state, name))
		}
		plan.Groups = append(plan.Groups, removal(state, target.name))
	}
	return plan, nil
}

func removal(state keenetic.State, name string) Removal {
	return Removal{name, state.Groups[name], state.Routes[name]}
}

func (r Removal) matches(state keenetic.State) bool {
	group, exists := state.Groups[r.Name]
	return exists && group.Description == r.group.Description &&
		group.HasExcludes == r.group.HasExcludes && sameDomains(group.Domains, r.group.Domains) &&
		slices.EqualFunc(state.Routes[r.Name], r.routes, keenetic.Route.Equal)
}
