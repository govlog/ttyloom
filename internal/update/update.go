// Package update tells which build is running and whether GitHub has a
// newer release of it.
package update

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

const repo = "govlog/ttyloom"

var (
	tagRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+(\.[0-9]+)?(-[0-9A-Za-z.]+)?$`) // same rule as scripts/release.sh
	shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Build : what the running binary was built from.
type Build struct {
	Version string    // tag of a release build ("v0.11-beta"), "dev" otherwise
	Commit  string    // full commit hash, "" when unknown
	Time    time.Time // commit time of a local build (VCS stamp), zero when unknown
}

// ID : the build id shown to the user, the first 8 hex digits of the commit.
func (b Build) ID() string {
	if b.Commit == "" {
		return "unknown"
	}
	return b.Commit[:8]
}

// Current : the build of this binary. The release script gives version and
// commit with -ldflags; a plain "go build" in a git checkout leaves them at
// their defaults and writes the commit and its time in the build info.
func Current(version, commit string) Build {
	b := Build{Version: version, Commit: commit}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if !shaRe.MatchString(b.Commit) {
					b.Commit = s.Value
				}
			case "vcs.time":
				b.Time, _ = time.Parse(time.RFC3339, s.Value)
			}
		}
	}
	if !shaRe.MatchString(b.Commit) {
		b.Commit = ""
	}
	return b
}

// Release : the newest published release.
type Release struct {
	Tag    string
	Commit string
	URL    string // made here from the tag, never taken from the answer
}

// ID : the build id of the release, like Build.ID.
func (r Release) ID() string { return r.Commit[:8] }

// Check asks GitHub for the newest published release and tells whether it is
// newer than b. api is the root of the REST API ("https://api.github.com").
// Two small requests: the release list (/releases/latest skips the
// pre-releases, and every beta is one), then the commit of its tag.
func Check(ctx context.Context, c *http.Client, api string, b Build) (Release, bool, error) {
	body, err := get(ctx, c, api+"/repos/"+repo+"/releases?per_page=5", "application/vnd.github+json")
	if err != nil {
		return Release{}, false, err
	}
	var list []struct {
		Tag       string    `json:"tag_name"`
		Draft     bool      `json:"draft"`
		Published time.Time `json:"published_at"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return Release{}, false, err
	}
	var rel Release
	var published time.Time
	for _, r := range list {
		if !r.Draft && tagRe.MatchString(r.Tag) && r.Published.After(published) {
			rel.Tag, published = r.Tag, r.Published
		}
	}
	if rel.Tag == "" {
		return Release{}, false, fmt.Errorf("GitHub: no release")
	}
	body, err = get(ctx, c, api+"/repos/"+repo+"/commits/"+rel.Tag, "application/vnd.github.sha")
	if err != nil {
		return Release{}, false, err
	}
	if rel.Commit = strings.TrimSpace(string(body)); !shaRe.MatchString(rel.Commit) {
		return Release{}, false, fmt.Errorf("GitHub: no commit for %s", rel.Tag)
	}
	rel.URL = "https://github.com/" + repo + "/releases/tag/" + rel.Tag
	return rel, newer(b, rel, published), nil
}

// newer : a release build compares versions; a local build compares its
// commit time with the publication of the release — a build of a commit made
// after it already holds what it brings.
func newer(b Build, rel Release, published time.Time) bool {
	switch {
	case b.Commit == rel.Commit:
		return false
	case tagRe.MatchString(b.Version):
		return compareVersions(rel.Tag, b.Version) > 0
	case !b.Time.IsZero():
		return published.After(b.Time)
	}
	return false
}

// compareVersions orders two tags: numbers first, then a final version
// before its pre-releases, then the pre-release names.
func compareVersions(a, b string) int {
	an, ap := parseVersion(a)
	bn, bp := parseVersion(b)
	for i := range an {
		if c := cmp.Compare(an[i], bn[i]); c != 0 {
			return c
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return strings.Compare(ap, bp)
}

func parseVersion(v string) (n [3]int, pre string) {
	v, pre, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, f := range strings.SplitN(v, ".", 3) {
		n[i], _ = strconv.Atoi(f)
	}
	return n, pre
}

// get reads one answer of the API, 1 MiB at most.
func get(ctx context.Context, c *http.Client, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ttyloom")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub: HTTP %d", resp.StatusCode) // the reason phrase is the server's text
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
