#!/bin/sh
# Owner-authorized release step: sign Phone Terminal.app with the owner's
# Developer ID, notarize and staple it, then build, sign, notarize and staple
# the DMG. Credentials never enter this repository: the signing identity stays
# in the login keychain and notarytool uses a stored keychain profile, named by
#   DEVELOPER_ID_APPLICATION     e.g. "Developer ID Application: Name (TEAMID)"
#   NOTARYTOOL_KEYCHAIN_PROFILE  created once with `xcrun notarytool store-credentials`
set -eu

if [ -z "${DEVELOPER_ID_APPLICATION:-}" ] || [ -z "${NOTARYTOOL_KEYCHAIN_PROFILE:-}" ]; then
  echo "Refusing to sign: set DEVELOPER_ID_APPLICATION and NOTARYTOOL_KEYCHAIN_PROFILE (owner authorization required)." >&2
  exit 1
fi

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_dir"
app="dist/macos/Phone Terminal.app"
if [ ! -d "$app" ]; then
  echo "Run packaging/macos/build-app.sh first." >&2
  exit 1
fi

sign() {
  codesign --force --timestamp --options runtime --sign "$DEVELOPER_ID_APPLICATION" "$@"
}
notarize() {
  xcrun notarytool submit "$1" --keychain-profile "$NOTARYTOOL_KEYCHAIN_PROFILE" --wait
}

sign "$app/Contents/MacOS/pterm"
sign "$app"
codesign --verify --strict --deep --verbose=2 "$app"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
ditto -c -k --keepParent "$app" "$work/app.zip"
notarize "$work/app.zip"
xcrun stapler staple "$app"

PTERM_DMG_LABEL=signed "$repo_dir/packaging/macos/build-dmg.sh"
dmg=$(ls -t artifacts/Phone-Terminal-*-macos-arm64.dmg | head -n 1)
codesign --force --timestamp --sign "$DEVELOPER_ID_APPLICATION" "$dmg"
notarize "$dmg"
xcrun stapler staple "$dmg"
(cd artifacts && shasum -a 256 "$(basename "$dmg")" >"$(basename "$dmg").sha256")

spctl --assess --type open --context context:primary-signature --verbose "$dmg"
spctl --assess --type execute --verbose "$app"
echo "Signed and notarized $dmg"
