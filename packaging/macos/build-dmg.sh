#!/bin/sh
# Package dist/macos/Termivoa.app into a compressed read-only DMG in
# artifacts/. The file name says "unsigned" unless PTERM_DMG_LABEL=signed.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_dir"

app="dist/macos/Termivoa.app"
if [ ! -d "$app" ]; then
  echo "Run packaging/macos/build-app.sh first." >&2
  exit 1
fi
version=$(plutil -extract PTermBuildID raw -o - "$app/Contents/Info.plist")
version=${version%%+*}
label=${PTERM_DMG_LABEL:-unsigned}
case $label in
  unsigned) suffix="-unsigned" ;;
  signed) suffix="" ;;
  *) echo "PTERM_DMG_LABEL must be unsigned or signed" >&2; exit 1 ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
ditto "$app" "$stage/Termivoa.app"
ln -s /Applications "$stage/Applications"

mkdir -p artifacts
dmg="artifacts/Termivoa-$version-macos-arm64$suffix.dmg"
rm -f "$dmg" "$dmg.sha256"
hdiutil create -quiet -volname "Termivoa" -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dmg"
(cd artifacts && shasum -a 256 "$(basename "$dmg")" >"$(basename "$dmg").sha256")

echo "Built $dmg"
