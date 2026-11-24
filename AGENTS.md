# Keenetic Geosite Sync

## Project structure

Keenetic Geosite Sync is a Go CLI for KeeneticOS 5 with Entware. It synchronizes DNS groups through `ndmc` and runs from SSH or cron.

- `cmd/keenetic-geosite-sync` contains the entry point. Command parsing and terminal output belong in `internal/cli`.
- `internal/config` validates the single JSON configuration object. A `v2fly` source uses the built-in upstream address and derives group descriptions from filenames. A `plain` source uses an explicit group description and HTTPS URL.
- `internal/lists` handles HTTPS downloads and plain files. `internal/geosite` handles v2fly syntax and includes.
- `internal/keenetic` reads router state and runs `ndmc`. `internal/syncer` builds and applies sync and cleanup plans. Keep planning independent of command execution.
- `opkg/` builds Entware packages and the package index. `.github/workflows/` contains CI and release automation.

## Behavior to preserve

- Use the same plan for a preview and an apply. Keep previews read-only and serialize operations that change the router.
- Use `kgs-` as the default prefix. During sync, prune groups for removed lists only within the configured prefix and the utility's generated naming scheme. Protect Keenetic's built-in naming patterns. Keep explicit cleanup scoped to configured lists. Preserve existing DNS routes and their flags when updating groups, and verify updates before deletions.
- Enforce the Keenetic group limit of 300 entries. Apply `ipv6: false` to explicit IPv6 addresses and CIDRs in plain lists.
- Report partial `ndmc` failures and return a nonzero exit code. Persist only after verifying the applied plan. Retry a pending save only when the running configuration still matches its fingerprint. Keep `validate` independent of `ndmc` and network requests.

## Verification

Run the Go tests and static checks before handing off a code change.

```sh
test -z "$(gofmt -l cmd internal)"
go test ./...
go vet ./...
```

Keep tests focused on regressions with user impact. Prioritize nested includes and filters, mixed IPv4 and IPv6 input, route preservation, cleanup scope, and command failures during an apply. Validate firmware-dependent behavior on Keenetic when changing router commands.

Run the packaging check on Linux with Go 1.24 or later and GNU tar.

```sh
opkg/build.sh 0.0.0 build/check
opkg/feed.sh build/check-pages build/check/*_0.0.0-1_aarch64-3.10.ipk build/check/*_0.0.0-1_mipsel-3.4.ipk
```

## Packaging and releases

- Publish `.ipk` packages for ARM64 and MIPS LE and declare the `ca-bundle` dependency. Keep `config.json` outside the package payload. Create `{}` on installation when the file is missing and preserve configurations from earlier packages during upgrades.
- A tag in the form `vMAJOR.MINOR.PATCH` sets the package version and triggers `.github/workflows/publish.yml`.
- Keep GitHub Release publication independent of the optional OPKG feed on GitHub Pages. Enable feed publication with the repository variable `PUBLISH_OPKG_FEED=true`. Pages must use **GitHub Actions** as its source. If the `github-pages` environment restricts deployment, allow release tags.
- Keep Pages artifact upload and deployment in separate jobs. Pass the uploaded artifact name to deployment so reruns can resolve the correct artifact.
- Keep the README focused on installation, configuration, and CLI usage for users.
