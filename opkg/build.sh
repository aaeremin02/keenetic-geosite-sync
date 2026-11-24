#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
package=keenetic-geosite-sync
version=${1:-}

if [[ $# -lt 1 || $# -gt 2 || ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([~+][0-9A-Za-z.]+)?$ ]]; then
  echo "Usage: $0 VERSION [OUTPUT_DIRECTORY]" >&2
  exit 2
fi

out=${2:-$root/build/out}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d "$out/.ipk.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
cd "$root"

build_package() {
  local goarch=$1 arch=$2
  local stage="$work/$arch"
  local control="$stage/control" data="$stage/data"
  local filename="${package}_${version}-1_${arch}.ipk"

  mkdir -p "$control" "$data/opt/bin"
  local -a target=(CGO_ENABLED=0 GOOS=linux "GOARCH=$goarch")
  if [[ "$goarch" == mipsle ]]; then
    target+=(GOMIPS=softfloat)
  fi
  env "${target[@]}" go build -trimpath -ldflags='-s -w' \
    -o "$data/opt/bin/$package" ./cmd/keenetic-geosite-sync
  install -D -m 0644 LICENSE "$data/opt/share/doc/$package/LICENSE"

  cat > "$control/control" <<CONTROL
Package: $package
Version: $version-1
Architecture: $arch
Maintainer: Keenetic Geosite Sync contributors
License: MIT
Section: net
Priority: optional
Depends: ca-bundle
Description: Sync Keenetic DNS object groups from HTTPS lists
CONTROL
  install -m 0755 opkg/preinst opkg/postinst "$control/"
  printf '2.0\n' > "$stage/debian-binary"
  tar --owner=0 --group=0 --numeric-owner -C "$control" -czf "$stage/control.tar.gz" .
  tar --owner=0 --group=0 --numeric-owner -C "$data" -czf "$stage/data.tar.gz" .
  tar --owner=0 --group=0 --numeric-owner -C "$stage" -czf "$work/$filename" \
    ./debian-binary ./control.tar.gz ./data.tar.gz
}

build_package arm64 aarch64-3.10
build_package mipsle mipsel-3.4

for ipk in "$work"/*.ipk; do
  mv -- "$ipk" "$out/"
  printf 'Built %s/%s\n' "$out" "${ipk##*/}"
done
