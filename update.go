package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repoOwner     = "nvquanghuy"
	repoName      = "repoview"
	releasesURL   = "https://api.github.com/repos/nvquanghuy/repoview/releases/latest"
	checkInterval = 24 * time.Hour
)

// Release represents a GitHub release.
type Release struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// UpdateState tracks update check timing.
type UpdateState struct {
	LastUpdateCheck time.Time `json:"lastUpdateCheck"`
}

// runUpdate handles the "update" subcommand.
func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "check for updates without installing")
	fs.Parse(args)

	// Get executable path early to show the user
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot resolve symlinks: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Current version: v%s\n", version)
	fmt.Printf("Executable: %s\n", execPath)
	fmt.Println()

	fmt.Println("Checking for updates...")
	release, err := checkLatestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking for updates: %v\n", err)
		os.Exit(1)
	}

	cmp := compareVersions(version, release.Version)
	if cmp >= 0 {
		fmt.Println("You're up to date!")
		return
	}

	fmt.Printf("Update available: v%s → %s\n", version, release.Version)

	if release.URL != "" {
		fmt.Printf("Details: %s\n", release.URL)
	}

	if release.Notes != "" {
		fmt.Println("\nRelease notes:")
		fmt.Println(release.Notes)
	}

	if *checkOnly {
		fmt.Println("\nRun 'repoview update' to install the update.")
		return
	}

	fmt.Printf("\nDownloading %s...\n", release.Version)
	if err := selfUpdate(release.DownloadURL); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully updated %s to %s!\n", execPath, release.Version)
}

// ReleaseInfo contains information about a GitHub release.
type ReleaseInfo struct {
	Version     string
	DownloadURL string
	Notes       string
	URL         string
}

// checkLatestVersion queries the GitHub API for the latest release.
func checkLatestVersion() (*ReleaseInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(releasesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}

	info := &ReleaseInfo{
		Version: release.TagName,
		Notes:   strings.TrimSpace(release.Body),
		URL:     release.HTMLURL,
	}

	// Find the right asset for this OS/arch
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	assetName := fmt.Sprintf("repoview-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			info.DownloadURL = asset.BrowserDownloadURL
			return info, nil
		}
	}

	return info, fmt.Errorf("no binary found for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// compareVersions compares two semver strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareVersions(a, b string) int {
	// Strip leading 'v' if present
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	// Handle dev version
	if a == "dev" {
		return -1
	}
	if b == "dev" {
		return 1
	}

	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		var numA, numB int
		if i < len(partsA) {
			numA, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			numB, _ = strconv.Atoi(partsB[i])
		}

		if numA < numB {
			return -1
		}
		if numA > numB {
			return 1
		}
	}

	return 0
}

// binaryName returns the expected binary name for the current OS.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "repoview.exe"
	}
	return "repoview"
}

// selfUpdate downloads and installs a new version.
func selfUpdate(downloadURL string) error {
	// Get path to current executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("cannot resolve symlinks: %w", err)
	}

	// Download to temp file
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Create temp file for the archive
	suffix := ".tar.gz"
	if runtime.GOOS == "windows" {
		suffix = ".zip"
	}
	tmpArchive, err := os.CreateTemp("", "repoview-*"+suffix)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpArchivePath := tmpArchive.Name()
	defer os.Remove(tmpArchivePath)

	if _, err := io.Copy(tmpArchive, resp.Body); err != nil {
		tmpArchive.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpArchive.Close()

	// Extract the binary from archive
	tmpDir, err := os.MkdirTemp("", "repoview-extract-*")
	if err != nil {
		return fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if runtime.GOOS == "windows" {
		if err := extractZip(tmpArchivePath, tmpDir); err != nil {
			return fmt.Errorf("extraction failed: %w", err)
		}
	} else {
		if err := extractTarGz(tmpArchivePath, tmpDir); err != nil {
			return fmt.Errorf("extraction failed: %w", err)
		}
	}

	// Find the binary in extracted files
	newBinaryPath := filepath.Join(tmpDir, binaryName())
	if _, err := os.Stat(newBinaryPath); err != nil {
		return fmt.Errorf("binary not found in archive: %w", err)
	}

	// On Windows, the running executable is locked, so we rename it to .old
	// instead of .backup (the .old file is cleaned up on next run).
	backupPath := execPath + ".old"
	// Clean up any leftover .old file from a previous update
	os.Remove(backupPath)

	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("cannot create backup: %w", err)
	}

	// Move new binary to executable path
	if err := copyFile(newBinaryPath, execPath); err != nil {
		// Try to restore backup
		os.Rename(backupPath, execPath)
		return fmt.Errorf("cannot install new binary: %w", err)
	}

	// Set executable permissions (no-op on Windows, but harmless)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(execPath, 0755); err != nil {
			// Try to restore backup
			os.Remove(execPath)
			os.Rename(backupPath, execPath)
			return fmt.Errorf("cannot set permissions: %w", err)
		}
	}

	// Remove backup (may fail on Windows if still running; cleaned up next time)
	os.Remove(backupPath)

	return nil
}

// extractTarGz extracts a .tar.gz file to a directory using pure Go.
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Only extract regular files
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Use only the base name to avoid path traversal
		outPath := filepath.Join(destDir, filepath.Base(hdr.Name))
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}
	return nil
}

// extractZip extracts a .zip file to a directory.
func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		// Use only the base name to avoid path traversal
		outPath := filepath.Join(destDir, filepath.Base(f.Name))
		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

// cleanupOldBinary removes leftover .old files from a previous update (Windows).
func cleanupOldBinary() {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	os.Remove(execPath + ".old")
}

// maybeCheckForUpdates runs an update check in the background if enough time has passed.
func maybeCheckForUpdates() {
	cleanupOldBinary()
	go func() {
		state := loadUpdateState()
		if time.Since(state.LastUpdateCheck) < checkInterval {
			return
		}

		release, err := checkLatestVersion()
		if err != nil {
			return
		}

		if compareVersions(version, release.Version) < 0 {
			fmt.Fprintf(os.Stderr, "\nUpdate available: v%s → %s\n", version, release.Version)
			if release.URL != "" {
				fmt.Fprintf(os.Stderr, "Details: %s\n", release.URL)
			}
			fmt.Fprintf(os.Stderr, "Run 'repoview update' to upgrade.\n\n")
		}

		state.LastUpdateCheck = time.Now()
		saveUpdateState(state)
	}()
}

// stateFilePath returns the path to the state file.
// This is a variable so it can be overridden in tests.
var stateFilePath = func() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "repoview", "state.json")
}

// loadUpdateState loads the update state from disk.
func loadUpdateState() UpdateState {
	var state UpdateState
	data, err := os.ReadFile(stateFilePath())
	if err != nil {
		return state
	}
	json.Unmarshal(data, &state)
	return state
}

// saveUpdateState saves the update state to disk.
func saveUpdateState(state UpdateState) {
	path := stateFilePath()
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)

	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0644)
}
