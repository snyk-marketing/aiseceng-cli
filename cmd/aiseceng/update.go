package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

// The update notice: aiseceng asks GitHub for its latest release at most once
// a day, and when that is newer than the running build, says so after a
// command or in the TUI's key bar. AISECENG_NO_UPDATE_CHECK turns it off. It
// stays quiet in CI and for builds without a release version, such as
// `go run`.

// releasesURL is the repo's releases page. GitHub redirects its /latest to the
// newest release's tag, which needs no token and has no API rate limit.
var releasesURL = "https://github.com/snyk-marketing/aiseceng-cli/releases"

// updateInterval is how long a check's answer is reused before asking again.
const updateInterval = 24 * time.Hour

// releaseClient stops at GitHub's redirect instead of following it, and gives
// up quickly so a slow network never holds up a command's exit for long.
var releaseClient = &http.Client{
	Timeout:       3 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// updateState is the last check's answer, kept in the user's cache directory.
type updateState struct {
	CheckedAt time.Time `json:"checkedAt"`
	Latest    string    `json:"latest"`
}

// noticeUpdates has cmd check for a newer release while it runs and mention
// one on stderr after its output. A stderr that is not a terminal, such as a
// script's log, gets neither the check nor the notice.
func noticeUpdates(cmd *cobra.Command) {
	var found <-chan string

	cmd.PreRun = func(cmd *cobra.Command, _ []string) {
		if term.IsTerminal(os.Stderr.Fd()) {
			found = checkForUpdate(cmd.Context())
		}
	}
	cmd.PostRun = func(cmd *cobra.Command, _ []string) {
		if found == nil {
			return
		}

		if latest := <-found; latest != "" {
			lipgloss.Fprintln(cmd.ErrOrStderr(), "\n"+updateNotice(latest))
		}
	}
}

// checkForUpdate looks for a newer release in the background, so the lookup
// overlaps the command's own fetches. The channel yields the newer tag, or ""
// when this build is current, has no release version, or the check is off.
func checkForUpdate(ctx context.Context) <-chan string {
	found := make(chan string, 1)

	go func() {
		current := currentVersion()
		dir, err := os.UserCacheDir()

		if current == "" || err != nil || os.Getenv("AISECENG_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
			found <- ""

			return
		}

		found <- newerRelease(ctx, current, filepath.Join(dir, "aiseceng", "update.json"))
	}()

	return found
}

// updateNotice is the line that says latest is out, and how to upgrade.
func updateNotice(latest string) string {
	notice := secondaryText.Render(fmt.Sprintf("aiseceng %s is out, you have %s: ", latest, currentVersion()))

	exe, _ := os.Executable()
	// Homebrew links bin/aiseceng to the binary in its Caskroom.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	if command := upgradeCommand(exe); command != "" {
		return notice + linkText.Render(command)
	}

	url := releasesURL + "/latest"

	return notice + linkText.Hyperlink(url).Render(url)
}

// upgradeCommand is the command that upgrades the binary at exe: Homebrew's
// for the cask, Go's for a `go install` build, or "" for a binary downloaded
// from the releases page.
func upgradeCommand(exe string) string {
	switch {
	case strings.Contains(filepath.ToSlash(exe), "/Caskroom/"):
		return "brew upgrade aiseceng"
	// Only a release build has its version set by GoReleaser.
	case version == "":
		return "go install github.com/snyk-marketing/aiseceng-cli/cmd/aiseceng@latest"
	default:
		return ""
	}
}

// currentVersion is the running build's version, such as v0.1.8, or "" for a
// build without a release version: `go run`, or a `go build` between tags.
func currentVersion() string {
	// GoReleaser's version drops the tag's v. A `go install` build has none,
	// but its module version is the tag it was installed at.
	v := "v" + version
	if version == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			v = info.Main.Version
		}
	}

	if !semver.IsValid(v) || semver.Prerelease(v) != "" {
		return ""
	}

	return v
}

// newerRelease is the latest release's tag when it is newer than current, or
// "". It asks GitHub only once the answer cached at statePath is a day old.
func newerRelease(ctx context.Context, current, statePath string) string {
	var state updateState

	if data, err := os.ReadFile(statePath); err == nil {
		json.Unmarshal(data, &state)
	}

	if time.Since(state.CheckedAt) >= updateInterval {
		// A failed check counts too, so being offline costs one attempt a day
		// rather than one a run.
		state.CheckedAt = time.Now()
		if latest, err := latestRelease(ctx); err == nil {
			state.Latest = latest
		}

		if data, err := json.Marshal(state); err == nil && os.MkdirAll(filepath.Dir(statePath), 0o755) == nil {
			os.WriteFile(statePath, data, 0o644)
		}
	}

	if semver.Compare(state.Latest, current) > 0 {
		return state.Latest
	}

	return ""
}

// latestRelease is the newest release's tag, read from where GitHub redirects
// releases/latest.
func latestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releasesURL+"/latest", nil)
	if err != nil {
		return "", err
	}

	res, err := releaseClient.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()

	location := res.Header.Get("Location")
	if tag := path.Base(location); semver.IsValid(tag) {
		return tag, nil
	}

	return "", fmt.Errorf("no release tag in %q", location)
}
