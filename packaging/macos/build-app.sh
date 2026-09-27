#!/bin/sh
# Build an UNSIGNED Termivoa.app for Apple Silicon into dist/macos.
# Signing is a separate owner-authorized step (sign-and-notarize.sh).
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_dir"

version=${PTERM_VERSION:-0.0.0-dev}
if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  echo "PTERM_VERSION must look like 1.2.3 or 1.2.3-rc.1" >&2
  exit 1
fi
short_version=${version%%-*}
build_id="$version+$(git rev-parse --short=12 HEAD)"
git diff --quiet HEAD -- || build_id="$build_id.dirty"

app="dist/macos/Termivoa.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

pnpm build:web
./scripts/embed-web.sh

export CGO_ENABLED=0 GOOS=darwin GOARCH=arm64
go build -trimpath -tags webdist -ldflags "-X main.buildID=$build_id" -o "$app/Contents/MacOS/pterm" ./cmd/pterm
go build -trimpath -o "$app/Contents/MacOS/Termivoa" ./packaging/macos/launcher

cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>Termivoa</string>
	<key>CFBundleExecutable</key>
	<string>Termivoa</string>
	<key>CFBundleIdentifier</key>
	<string>dev.phone-terminal.companion</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>Termivoa</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$short_version</string>
	<key>CFBundleVersion</key>
	<string>$short_version</string>
	<key>LSArchitecturePriority</key>
	<array>
		<string>arm64</string>
	</array>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>PTermBuildID</key>
	<string>$build_id</string>
</dict>
</plist>
EOF
printf 'APPL????' >"$app/Contents/PkgInfo"
plutil -lint "$app/Contents/Info.plist" >/dev/null

echo "Built unsigned $app ($build_id)"
