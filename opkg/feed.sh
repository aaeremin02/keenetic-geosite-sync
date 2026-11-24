#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 3 ]]; then
  echo "Usage: $0 OUTPUT_DIRECTORY ARM64_IPK MIPSLE_IPK" >&2
  exit 2
fi

out=$1
mkdir -p "$out"
stage=$(mktemp -d "$out/.feed.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT

write_package() {
  local file=$1 arch=$2 directory=$3
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
  cp -- "$file" "$directory/"
  size=$(wc -c < "$file")
  md5=$(md5sum < "$file")
  sha256=$(sha256sum < "$file")
  printf '%s\nFilename: %s\nSize: %s\nMD5Sum: %s\nSHA256sum: %s\n\n' \
    "$control" "$filename" "$size" "${md5%% *}" "${sha256%% *}"
}

write_feed() {
  local file=$1 arch=$2
  local directory="$stage/$arch" filename=${file##*/}
  mkdir -p "$directory"
  write_package "$file" "$arch" "$directory" > "$directory/Packages"
  gzip -n -c "$directory/Packages" > "$directory/Packages.gz"

  cat > "$directory/index.html" <<HTML
<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Keenetic Geosite Sync packages</title>
<h1>Entware packages for $arch</h1>
<ul>
<li><a href="Packages">Packages</a></li>
<li><a href="Packages.gz">Packages.gz</a></li>
<li><a href="$filename">$filename</a></li>
</ul>
</html>
HTML
}

write_feed "$2" aarch64-3.10
write_feed "$3" mipsel-3.4

cat > "$stage/index.html" <<'HTML'
<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Keenetic Geosite Sync</title>
<h1>Keenetic Geosite Sync</h1>
<ul>
<li><a href="aarch64-3.10/">ARM64</a></li>
<li><a href="mipsel-3.4/">MIPS LE</a></li>
</ul>
</html>
HTML

for arch in aarch64-3.10 mipsel-3.4; do
  mkdir -p "$out/$arch"
  mv -- "$stage/$arch"/* "$out/$arch/"
done
mv -- "$stage/index.html" "$out/"
