# RepoView macOS App

A native macOS wrapper for RepoView, providing a standalone app experience for viewing markdown files and browsing repositories.

## Features

- Native macOS app with familiar window controls
- Folder picker on first launch (remembers last folder)
- Embedded WebView for the RepoView interface
- Menu bar integration with keyboard shortcuts
- "Open in Browser" option for power users

## Requirements

- macOS 13.0 (Ventura) or later
- Xcode 15.0 or later (for building)
- Go 1.21 or later (for building the repoview binary)

## Building

### Quick Build

```bash
cd macos
chmod +x build.sh
./build.sh
```

This builds a debug version. The app will be at:
`macos/RepoView/build/Build/Products/Debug/RepoView.app`

### Release Build

```bash
./build.sh --release
```

Creates a zipped app ready for distribution at:
`macos/RepoView/build/Build/Products/Release/RepoView.zip`

### Manual Build with Xcode

1. Build the Go binary:
   ```bash
   cd /path/to/repoview
   GOOS=darwin GOARCH=arm64 go build -o macos/repoview .
   ```

2. Open `macos/RepoView/RepoView.xcodeproj` in Xcode

3. Build and run (Cmd+R)

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Cmd+O | Open folder |
| Cmd+R | Reload |
| Cmd+Shift+B | Open in browser |
| Cmd+W | Close window |
| Cmd+Q | Quit |

Plus all the existing RepoView shortcuts work within the WebView:
- `t` - Fuzzy file search
- `Alt+C` - Collapse all sections
- `Alt+E` - Expand all sections

## Distribution

### Unsigned (for testing)

The built `.app` or `.zip` can be shared directly. Users will need to:
1. Right-click the app
2. Select "Open"
3. Click "Open" in the security dialog

### Signed & Notarized (for public distribution)

1. Add your Apple Developer Team ID to the Xcode project
2. Enable "Hardened Runtime" (already configured)
3. Archive and notarize through Xcode Organizer

## Customization

### Bundle Identifier

Edit `RepoView.xcodeproj` or `Info.plist` to change from `com.repoview.app` to your own identifier.

### App Icon

Replace the placeholder in `RepoView/Assets.xcassets/AppIcon.appiconset/` with your icon images (sizes: 16, 32, 128, 256, 512 @1x and @2x).

## Architecture

```
RepoView.app/
  Contents/
    MacOS/
      RepoView        # Swift launcher
      repoview        # Go binary (bundled)
    Resources/
      Assets.car      # App icon
    Info.plist
```

The Swift app:
1. Checks for a saved folder path (UserDefaults)
2. Shows folder picker if none saved
3. Starts the Go repoview server on an available port
4. Displays the UI in a WKWebView
5. Terminates the server when the app quits
