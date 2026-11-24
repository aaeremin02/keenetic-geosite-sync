package syncer

import (
	"context"
	"fmt"
	"slices"

	"keenetic-geosite-sync/internal/keenetic"
)

type Router interface {
	ReadRunningConfig(context.Context) (keenetic.State, error)
	RunCommand(context.Context, string) error
}

type ApplyResult struct {
	Commands    int
	Fingerprint keenetic.Fingerprint
}

func ApplySync(ctx context.Context, plan Plan, router Router) (ApplyResult, error) {
	var result ApplyResult
	commands, err := updateCommands(plan)
	if err != nil {
		return result, err
	}
	var removals []Removal
	for _, list := range plan.Lists {
		removals = append(removals, list.Stale...)
	}

	deletions, err := deleteCommands(removals)
	if err != nil || len(commands)+len(deletions) == 0 {
		return result, err
	}

	result.Commands, err = runCommands(ctx, router, commands)
	if err != nil {
		return result, err
	}
	state, err := router.ReadRunningConfig(ctx)
	if err != nil {
		return result, fmt.Errorf("check updated groups: %w", err)
	}
	if err := verifySync(plan, state); err != nil {
		return result, err
	}

	deleted, final, err := applyDeletions(ctx, router, state, removals, deletions)
	result.Commands += deleted
	if err != nil {
		return result, err
	}
	result.Fingerprint = final.Fingerprint
	return result, nil
}

func ApplyCleanup(ctx context.Context, plan CleanupPlan, router Router) (ApplyResult, error) {
	var result ApplyResult
	deletions, err := deleteCommands(plan.Groups)
	if err != nil || len(deletions) == 0 {
		return result, err
	}
	state, err := router.ReadRunningConfig(ctx)
	if err != nil {
		return result, fmt.Errorf("check groups before cleanup: %w", err)
	}
	deleted, final, err := applyDeletions(ctx, router, state, plan.Groups, deletions)
	result.Commands = deleted
	if err != nil {
		return result, err
	}
	result.Fingerprint = final.Fingerprint
	return result, nil
}

func updateCommands(plan Plan) ([]string, error) {
	var create, update []string
	for _, list := range plan.Lists {
		for _, group := range list.Groups {
			commands, err := groupCommands(group)
			if err != nil {
				return nil, err
			}
			if group.Change == "create" {
				create = append(create, commands...)
			} else {
				update = append(update, commands...)
			}
		}
	}
	// Populate new chunks before removing entries that moved out of an existing group.
	return append(create, update...), nil
}

func groupCommands(group GroupPlan) ([]string, error) {
	var commands []string
	if group.Change == "create" {
		command, err := keenetic.CreateGroupCommand(group.Name)
		if err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	if group.setDescription {
		command, err := keenetic.DescribeGroupCommand(group.Name, group.Description)
		if err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}

	before, err := includeCommands(group.Name, group.remove[:group.makeRoom], true)
	if err != nil {
		return nil, err
	}
	add, err := includeCommands(group.Name, group.add, false)
	if err != nil {
		return nil, err
	}
	after, err := includeCommands(group.Name, group.remove[group.makeRoom:], true)
	if err != nil {
		return nil, err
	}
	commands = append(commands, before...)
	commands = append(commands, add...)
	commands = append(commands, after...)

	for _, route := range group.Routes {
		if route.Exists || route.Disabled {
			continue
		}
		command, err := keenetic.RouteCommand(group.Name, route.Route)
		if err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func includeCommands(group string, addresses []string, remove bool) ([]string, error) {
	var commands []string
	for _, address := range addresses {
		command, err := keenetic.IncludeCommand(group, address, remove)
		if err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func deleteCommands(removals []Removal) ([]string, error) {
	var commands []string
	for _, target := range removals {
		command, err := keenetic.DeleteGroupCommand(target.Name)
		if err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func runCommands(ctx context.Context, router Router, commands []string) (int, error) {
	for i, command := range commands {
		if err := router.RunCommand(ctx, command); err != nil {
			return i, err
		}
	}
	return len(commands), nil
}

func verifySync(plan Plan, state keenetic.State) error {
	for _, list := range plan.Lists {
		for _, group := range list.Groups {
			current, ok := state.Groups[group.Name]
			if !ok || current.Description != group.Description || !sameDomains(current.Domains, group.Domains) {
				return fmt.Errorf("Keenetic did not apply the planned changes to group %s", group.Name)
			}
			for _, route := range group.Routes {
				if route.Exists || !route.Disabled {
					if !slices.ContainsFunc(state.Routes[group.Name], route.Equal) {
						return fmt.Errorf("expected DNS route for %s through %s %s is missing or changed",
							group.Name, route.Gateway, route.Interface)
					}
				}
			}
		}
	}
	return nil
}

func applyDeletions(ctx context.Context, router Router, state keenetic.State, removals []Removal, commands []string) (int, keenetic.State, error) {
	if len(commands) == 0 {
		return 0, state, nil
	}
	for _, removal := range removals {
		if !removal.matches(state) {
			return 0, state, fmt.Errorf("group %s or its DNS routes changed after the plan was built", removal.Name)
		}
	}
	applied, err := runCommands(ctx, router, commands)
	if err != nil {
		return applied, state, err
	}
	state, err = router.ReadRunningConfig(ctx)
	if err != nil {
		return applied, state, fmt.Errorf("check removed groups: %w", err)
	}
	for _, removal := range removals {
		if _, ok := state.Groups[removal.Name]; ok || len(state.Routes[removal.Name]) > 0 {
			return applied, state, fmt.Errorf("group %s or its DNS routes are still present", removal.Name)
		}
	}
	return applied, state, nil
}
