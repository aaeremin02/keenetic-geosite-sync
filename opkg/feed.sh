#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 3 ]]; then
  echo "Usage: $0 OUTPUT_DIRECTORY ARM64_IPK MIPSLE_IPK" >&2
  exit 2
fi

out=$1
shift
mkdir -p "$out/all"
stage=$(mktemp -d "$out/.feed.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT

write_package() {
  local file=$1 arch=$2
  local filename=${file##*/} control size md5 sha256
  if [[ ! $filename =~ ^keenetic-geosite-sync_[0-9][0-9A-Za-z.+~-]*_[0-9A-Za-z.-]+\.ipk$ ||
        "$filename" != *"_$arch.ipk" ]]; then
    echo "Unexpected filename for $arch: $filename" >&2
    return 1
  fi

  control=$(tar -xOzf "$file" ./control.tar.gz | tar -xzO ./control)
  if ! grep -Fxq "Architecture: $arch" <<< "$control"; then
    echo "Package architecture does not match $arch: $filename" >&2
    return 1
  fi
  cp -- "$file" "$stage/"
  size=$(wc -c < "$file")
  md5=$(md5sum < "$file")
  sha256=$(sha256sum < "$file")
  printf '%s\nFilename: %s\nSize: %s\nMD5Sum: %s\nSHA256sum: %s\n\n' \
    "$control" "$filename" "$size" "${md5%% *}" "${sha256%% *}"
}

{
  write_package "$1" aarch64-3.10
  write_package "$2" mipsel-3.4
} > "$stage/Packages"
gzip -n -c "$stage/Packages" > "$stage/Packages.gz"

cat > "$out/index.html" <<'HTML'
<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Keenetic Geosite Sync</title>
<h1>Keenetic Geosite Sync</h1>
<a href="all/">Entware packages</a>
</html>
HTML

{
  cat <<'HTML'
<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Keenetic Geosite Sync packages</title>
<h1>Entware packages</h1>
<ul>
<li><a href="Packages">Packages</a></li>
<li><a href="Packages.gz">Packages.gz</a></li>
HTML
  for ipk in "$stage"/*.ipk; do
    name=${ipk##*/}
    printf '<li><a href="%s">%s</a></li>\n' "$name" "$name"
  done
  printf '</ul>\n</html>\n'
} > "$stage/index.html"

mv -- "$stage"/* "$out/all/"
