package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"keenetic-geosite-sync/internal/config"
	"keenetic-geosite-sync/internal/keenetic"
	"keenetic-geosite-sync/internal/lists"
	"keenetic-geosite-sync/internal/syncer"
)

func Run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(out, "Usage: keenetic-geosite-sync sync [--dry-run] [--config PATH]")
		fmt.Fprintln(out, "       keenetic-geosite-sync clean (--dry-run | --apply) [--config PATH]")
		fmt.Fprintln(out, "       keenetic-geosite-sync validate [--config PATH]")
		return 0
	}

	command := args[0]
	if command != "sync" && command != "clean" && command != "validate" {
		fmt.Fprintf(stderr, "unknown command %q; see --help\n", command)
		return 2
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath(), "path to config.json")
	var dryRun, apply bool
	if command != "validate" {
		flags.BoolVar(&dryRun, "dry-run", false, "show planned changes")
	}
	if command == "clean" {
		flags.BoolVar(&apply, "apply", false, "remove groups selected by the cleanup plan")
	}
	if err := flags.Parse(args[1:]); err == flag.ErrHelp {
		return 0
	} else if err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments; see %s --help\n", command)
		return 2
	}
	if command == "clean" && dryRun == apply {
		fmt.Fprintln(stderr, "clean requires exactly one of --dry-run or --apply")
		return 2
	}

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if command == "validate" {
		fmt.Fprintln(out, "Configuration valid")
		return 0
	}

	router := keenetic.Client{}
	var saver *configurationSaver
	if !dryRun && !cfg.DryRun {
		lock, err := acquireLock()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer lock.Close()

		saver = &configurationSaver{lock, router.RunCommand}
	}

	state, err := router.ReadRunningConfig(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if saver != nil {
		message, err := saver.resume(ctx, state.Fingerprint)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if message != "" {
			fmt.Fprintln(out, message)
		}
	}

	if command == "clean" {
		return clean(ctx, cfg, state, router, saver, out, stderr)
	}
	return sync(ctx, cfg, state, router, saver, out, stderr)
}

func sync(ctx context.Context, cfg config.Config, state keenetic.State, router syncer.Router,
	saver *configurationSaver, out, stderr io.Writer,
) int {
	resolver, err := lists.NewResolver(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan, err := syncer.BuildPlan(ctx, cfg, state, resolver.Resolve)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printPlan(out, plan, saver == nil)
	if saver == nil {
		return 0
	}
	applied, err := syncer.ApplySync(ctx, plan, router)
	return finishApply(ctx, saver, out, stderr, applied, err)
}

func clean(ctx context.Context, cfg config.Config, state keenetic.State, router syncer.Router,
	saver *configurationSaver, out, stderr io.Writer,
) int {
	plan, err := syncer.BuildCleanup(cfg, state)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printMode(out, saver == nil)
	if len(plan.Groups) == 0 {
		fmt.Fprintln(out, "No groups selected for cleanup")
	}
	for _, target := range plan.Groups {
		fmt.Fprintf(out, "  remove group %s\n", target.Name)
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(out, "  note: %s\n", warning)
	}
	if saver == nil {
		return 0
	}
	applied, err := syncer.ApplyCleanup(ctx, plan, router)
	return finishApply(ctx, saver, out, stderr, applied, err)
}

func finishApply(ctx context.Context, saver *configurationSaver, out, stderr io.Writer, result syncer.ApplyResult, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "Stopped after %d successful commands: %v\n", result.Commands, err)
		fmt.Fprintln(stderr, "Check the running configuration before retrying.")
		return 1
	}
	if result.Commands == 0 {
		fmt.Fprintln(out, "Already up to date")
		return 0
	}
	if err := saver.save(ctx, result.Fingerprint); err != nil {
		fmt.Fprintf(stderr, "Router changes verified (%d commands), but configuration persistence failed: %v\n", result.Commands, err)
		return 1
	}
	fmt.Fprintf(out, "Configuration saved (%d commands)\n", result.Commands)
	return 0
}

func defaultConfigPath() string {
	const installed = "/opt/keenetic-geosite-sync/config.json"
	if _, err := os.Stat(installed); err == nil || !os.IsNotExist(err) {
		return installed
	}
	if binary, err := os.Executable(); err == nil {
		adjacent := filepath.Join(filepath.Dir(binary), "config.json")
		if _, err := os.Stat(adjacent); err == nil {
			return adjacent
		}
	}
	return "config.json"
}

func printMode(out io.Writer, preview bool) {
	if preview {
		fmt.Fprintln(out, "Dry run")
	} else {
		fmt.Fprintln(out, "Applying plan")
	}
}

func printPlan(out io.Writer, plan syncer.Plan, preview bool) {
	printMode(out, preview)
	if len(plan.Lists) == 0 {
		fmt.Fprintln(out, "No domain lists selected")
	}
	for _, list := range plan.Lists {
		fmt.Fprintf(out, "%s (skipped %d keyword, %d regexp", list.Name, list.SkippedKeyword, list.SkippedRegexp)
		if list.SkippedIPv6 > 0 {
			fmt.Fprintf(out, ", %d IPv6", list.SkippedIPv6)
		}
		fmt.Fprintln(out, ")")
		for _, group := range list.Groups {
			added, removed := group.DomainChanges()
			fmt.Fprintf(out, "  %s %s (%q, %d entries", group.Change, group.Name, group.Description, len(group.Domains))
			if added != 0 || removed != 0 {
				fmt.Fprintf(out, ", +%d -%d", added, removed)
			}
			fmt.Fprintln(out, ")")
			for _, route := range group.Routes {
				action := "add"
				switch {
				case route.Exists:
					action = "keep"
				case route.Disabled:
					action = "skip disabled template for"
				}
				args := []string{route.Gateway, route.Interface, strings.Join(route.Options, " ")}
				if route.Disabled {
					args = append(args, "disabled")
				}
				fmt.Fprintf(out, "    %s route %s\n", action, strings.Join(strings.Fields(strings.Join(args, " ")), " "))
			}
		}
		for _, group := range list.Stale {
			fmt.Fprintf(out, "  remove stale group %s after successful update\n", group.Name)
		}
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(out, "  note: %s\n", warning)
	}
}
