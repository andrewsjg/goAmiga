#!/bin/sh
# Builds goAmiga.app: the goamiga binary in a macOS application bundle with
# the Boing Ball icon, so it shows in Finder and the Dock and can be put in
# /Applications. Run from the repository root; the app lands in the root.
#
#   scripts/macos-app.sh
set -e
APP=goAmiga.app
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

go build -o "$WORK/goamiga" ./cmd/goamiga
go run ./internal/boing/mkiconset "$WORK/goamiga.iconset"
iconutil -c icns -o "$WORK/goamiga.icns" "$WORK/goamiga.iconset"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$WORK/goamiga" "$APP/Contents/MacOS/goamiga"
cp "$WORK/goamiga.icns" "$APP/Contents/Resources/goamiga.icns"
VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>goAmiga</string>
	<key>CFBundleDisplayName</key><string>goAmiga</string>
	<key>CFBundleIdentifier</key><string>com.github.andrewsjg.goamiga</string>
	<key>CFBundleExecutable</key><string>goamiga</string>
	<key>CFBundleIconFile</key><string>goamiga</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
</dict>
</plist>
PLIST
echo "built $APP"
