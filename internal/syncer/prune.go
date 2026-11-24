package syncer

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"keenetic-geosite-sync/internal/keenetic"
)

var (
	generatedGroupSuffix = regexp.MustCompile(`^(0|[1-9][0-9]*)(?:-([2-9]|[1-9][0-9]+))?$`)
	keeneticGroupName    = regexp.MustCompile(`^(?:domain-list|domain-name-list-)[0-9]+$`)
)

func planPrunedGroups(prefix string, state keenetic.State, targets []target) ([]Removal, []string) {
	active := make(map[string]bool, len(targets))
	for _, target := range targets {
		active[target.name] = true
	}

	var removals []Removal
	var warnings []string
	// Remove parts before their base so a retry can still identify the remaining parts.
	for _, name := range slices.Backward(sortedNames(state.Groups)) {
		suffix, inScope := strings.CutPrefix(name, prefix)
		if !inScope {
			continue
		}
		match := generatedGroupSuffix.FindStringSubmatch(suffix)
		if match == nil {
			continue
		}
		base := prefix + match[1]
		baseGroup, exists := state.Groups[base]
		if active[base] || !exists || baseGroup.Description == "" || keeneticGroupName.MatchString(base) {
			continue
		}
		if baseGroup.HasExcludes {
			if name == base {
				warnings = append(warnings, fmt.Sprintf("keep %s and its parts: group has exclusions", base))
			}
			continue
		}
		if name != base && !canPrunePart(state, base, name, match[2]) {
			warnings = append(warnings, fmt.Sprintf("keep %s: unrelated description, exclusions or independent route", name))
			continue
		}
		removals = append(removals, removal(state, name))
	}
	return removals, warnings
}

func canPrunePart(state keenetic.State, base, name, part string) bool {
	group := state.Groups[name]
	if group.HasExcludes || hasIndependentRoute(state.Routes[name], state.Routes[base]) {
		return false
	}
	number, err := strconv.Atoi(part)
	if err != nil {
		return false
	}
	description := state.Groups[base].Description
	if title, first := splitDescription(description); first == 1 && isPart(group, title, number) {
		return true
	}
	return isPart(group, description, number)
}
