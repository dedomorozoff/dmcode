// Package update asks GitHub for the newest dmcode release and installs it.
//
// It is a package of its own because the answer is entirely platform-specific:
// a release publishes one binary per platform and names it after that platform,
// and the binary that is running cannot be overwritten while it runs on
// Windows. None of that belongs in the terminal interface, which has only to
// say what happened.
//
// The release is the same one install.sh fetches, down to the asset names: two
// updaters that look for different files are two updaters, and one of them
// breaks on the day a release is published.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	// Repo is the GitHub repository dmcode is released from. Every URL the
	// package builds is built from it, so there is one place to change if the
	// project moves.
	Repo = "dedomorozoff/dmcode"

	// AssetPrefix is the name every published binary starts with.
	AssetPrefix = "dmcode"

	// ChecksumsName is the release asset holding the sha256 of every other
	// asset. It is optional: a release from before it existed installs
	// without the check, and says that it did.
	ChecksumsName = "sha256sums.txt"
)

// Client asks GitHub about releases.
//
// The API address is a field rather than a constant so a test can point it at
// an httptest server. The download addresses come out of the release itself,
// so they need no second base: the release says where its files live.
type Client struct {
	// HTTP is the client every request goes through. A nil one means a default
	// with a timeout: a hung request is a hung command, and the interface is
	// the only place the user can see it happen.
	HTTP *http.Client
	// API is where the release API lives.
	API string
	// Repo is owner/name of the repository.
	Repo string
	// Token authenticates the API calls. It is optional — an anonymous call
	// works, it is just counted against a smaller rate limit — and it comes
	// from the environment rather than from a question.
	Token string
}

// New is the client the interface uses: GitHub's API, a token from the
// environment if there is one, and no client-wide timeout, because the
// deadline of a download of a binary that weighs tens of megabytes is not the
// deadline of the API call that found it.
func New() *Client {
	return &Client{
		API:   "https://api.github.com",
		Repo:  Repo,
		Token: firstEnv("GITHUB_TOKEN", "GH_TOKEN"),
	}
}

// firstEnv returns the first of the named variables that is set.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// Release is what GitHub says about a published release.
type Release struct {
	// Tag is the git tag the release was cut from, "v0.1.1".
	Tag string
	// Notes is the release body — the project's own account of what changed.
	Notes string
	// URL is the page a human can open.
	URL string
	// Assets are the published files.
	Assets []Asset
}

// Version is the release's version without the leading "v": the number the
// binary itself reports, so the two can be compared.
func (r Release) Version() string {
	return strings.TrimPrefix(r.Tag, "v")
}

// Asset is one published file of a release.
type Asset struct {
	// Name is the file name, "dmcode-linux-amd64".
	Name string
	// URL is where the file is downloaded from.
	URL string
	// Size is how many bytes it weighs, when GitHub says.
	Size int64
}

// FindAsset returns the published binary for a platform.
func (r Release) FindAsset(goos, goarch string) (Asset, bool) {
	want := AssetName(goos, goarch)
	for _, a := range r.Assets {
		if a.Name == want {
			return a, true
		}
	}
	return Asset{}, false
}

// FindAssetByName returns a published file by its name.
func (r Release) FindAssetByName(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the published binary for a platform: "dmcode-linux-amd64",
// "dmcode-windows-amd64.exe".
//
// The spelling is the one the release workflow publishes and the one
// install.sh already fetches, so the installer and the updater are looking for
// the same file.
func AssetName(goos, goarch string) string {
	name := AssetPrefix + "-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// releaseJSON is the part of GitHub's release payload this package reads.
// Only the fields that are used are named: a payload is a contract, and naming
// the rest of it would be a promise to keep it.
type releaseJSON struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Latest asks GitHub for the newest published release.
//
// The call is bounded by the context rather than by the client, so a caller
// that wants a quick answer and a caller that wants to wait can both use it.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(c.api(), "/"), c.repo())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub rejects a request without a user agent, and the honest one names
	// the program that made it.
	req.Header.Set("User-Agent", AssetPrefix)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, c.statusError(resp)
	}
	var raw releaseJSON
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Release{}, fmt.Errorf("cannot read the release: %w", err)
	}
	if raw.TagName == "" {
		return Release{}, fmt.Errorf("the release has no tag")
	}
	rel := Release{Tag: raw.TagName, Notes: raw.Body, URL: raw.HTMLURL}
	for _, a := range raw.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel, nil
}

// statusError turns a refusal into something a user can act on.
//
// The two that matter are told apart: a rate limit is a "try again later", and
// a repository with no release is a "there is nothing to install", and they
// are not the same problem.
func (c *Client) statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return fmt.Errorf("GitHub rate limit reached — try again in a few minutes")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("no release found for %s", c.repo())
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("GitHub answered %s: %s", resp.Status, msg)
}

// api is the API address, with the default filled in.
func (c *Client) api() string {
	if strings.TrimSpace(c.API) == "" {
		return "https://api.github.com"
	}
	return c.API
}

// repo is the repository, with the default filled in.
func (c *Client) repo() string {
	if strings.TrimSpace(c.Repo) == "" {
		return Repo
	}
	return c.Repo
}

// http is the HTTP client, with the default filled in.
func (c *Client) http() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: 10 * time.Minute}
	}
	return c.HTTP
}

// ChecksumFor returns the sha256 a release published for an asset.
//
// The file is the output of sha256sum: one line per asset, "<hash>  <name>",
// with a '*' in front of the name in binary mode. Both spellings are accepted
// because the release writes one of them and an installer must not care which.
func ChecksumFor(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.TrimPrefix(fields[0], "*"), true
		}
	}
	return "", false
}

// Compare orders two versions: -1 when a is older than b, 1 when it is newer,
// 0 when they are the same.
//
// The comparison is numeric where a number is there, because "0.1.10" is newer
// than "0.1.9" and a string comparison says the opposite. A part that carries
// a suffix is a pre-release, and a pre-release is older than the release it
// precedes: "0.2.0-rc1" is below "0.2.0".
func Compare(a, b string) int {
	as, bs := versionParts(a), versionParts(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y string
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if c := comparePart(x, y); c != 0 {
			return c
		}
	}
	return 0
}

// versionParts splits a version into its dotted parts.
func versionParts(v string) []string {
	return strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
}

// comparePart compares one version part: the number first, then the suffix.
//
// A missing part is a zero, so "0.2" and "0.2.0" are the same version — the
// two spellings of one release must not look like an upgrade.
func comparePart(x, y string) int {
	xn, xs := splitNumber(x)
	yn, ys := splitNumber(y)
	if xn != yn {
		if xn < yn {
			return -1
		}
		return 1
	}
	switch {
	case xs == ys:
		return 0
	case xs == "":
		// The final release is newer than the pre-release it followed.
		return 1
	case ys == "":
		return -1
	default:
		return strings.Compare(xs, ys)
	}
}

// splitNumber reads the leading number of a version part and what follows it.
func splitNumber(s string) (int, string) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, s
	}
	n := 0
	for _, c := range s[:end] {
		n = n*10 + int(c-'0')
	}
	return n, s[end:]
}

// Platform is the machine the update is for. It is a pair rather than a
// struct because the release names its files after exactly these two.
func Platform() (goos, goarch string) {
	return runtime.GOOS, runtime.GOARCH
}
