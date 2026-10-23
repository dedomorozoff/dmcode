package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The names are a contract with the release workflow: install.sh fetches
// "dmcode-linux-amd64" and the updater has to ask for the same file, or one
// of the two breaks on the day a release is published.
func TestAssetNameMatchesWhatTheReleasePublishes(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "dmcode-linux-amd64"},
		{"linux", "arm64", "dmcode-linux-arm64"},
		{"darwin", "arm64", "dmcode-darwin-arm64"},
		{"windows", "amd64", "dmcode-windows-amd64.exe"},
	} {
		if got := AssetName(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

// TestLatestReadsTheRelease drives the whole answer out of the API: the tag,
// the notes and the file that carries the binary.
func TestLatestReadsTheRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("asked for %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"tag_name":"v0.2.0","body":"what changed","html_url":"https://example/rel",
			"assets":[
				{"name":"dmcode-linux-amd64","browser_download_url":"%s/dl","size":42},
				{"name":"sha256sums.txt","browser_download_url":"%s/sums"}]}`,
			srvURL(r), srvURL(r))
	}))
	defer srv.Close()

	c := &Client{API: srv.URL, Repo: Repo}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Tag != "v0.2.0" || rel.Version() != "0.2.0" {
		t.Errorf("tag %q, version %q — want v0.2.0 / 0.2.0", rel.Tag, rel.Version())
	}
	if rel.Notes != "what changed" {
		t.Errorf("notes are %q", rel.Notes)
	}
	a, ok := rel.FindAsset("linux", "amd64")
	if !ok || a.Size != 42 {
		t.Errorf("the linux asset is %+v, found %v", a, ok)
	}
	if _, ok := rel.FindAsset("plan9", "mips"); ok {
		t.Error("a platform with no build was found")
	}
}

// srvURL is the address of the test server, taken out of the request that
// reached it: the handler cannot close over the server it is serving on.
func srvURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestTheChecksumsAreRead(t *testing.T) {
	sums := []byte("abc123  dmcode-linux-amd64\ndef456 *dmcode-windows-amd64.exe\n")
	for _, tc := range []struct {
		name string
		want string
		ok   bool
	}{
		{"dmcode-linux-amd64", "abc123", true},
		{"dmcode-windows-amd64.exe", "def456", true},
		{"dmcode-darwin-arm64", "", false},
	} {
		got, ok := ChecksumFor(sums, tc.name)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ChecksumFor(%q) = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestInstallPutsTheBinaryInPlace is the whole point: the file at the path is
// the one from the release, and it was checked against the release's own
// checksum before it was allowed there.
func TestInstallPutsTheBinaryInPlace(t *testing.T) {
	payload := []byte("a brand new dmcode")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl":
			w.Write(payload)
		case "/sums":
			fmt.Fprintf(w, "%s  dmcode-linux-amd64\n", hex.EncodeToString(sum[:]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := Release{Tag: "v0.2.0", Assets: []Asset{
		{Name: "dmcode-linux-amd64", URL: srv.URL + "/dl"},
		{Name: ChecksumsName, URL: srv.URL + "/sums"},
	}}
	dest := filepath.Join(t.TempDir(), "dmcode")
	c := &Client{API: srv.URL}
	res, err := Install(context.Background(), c, rel, dest)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Verified {
		t.Error("the download was not verified against the release's checksum")
	}
	if res.Version != "0.2.0" {
		t.Errorf("installed %q", res.Version)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the binary is not at the path: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("the file holds %q", got)
	}
}

// TestInstallRefusesAMismatchedChecksum: a download that does not match the
// release's checksum is not installed at all. The old binary stays, which is
// the point — a corrupted download must not replace a working one.
func TestInstallRefusesAMismatchedChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl":
			w.Write([]byte("something else"))
		case "/sums":
			fmt.Fprintf(w, "%s  dmcode-linux-amd64\n", strings.Repeat("0", 64))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := Release{Tag: "v0.2.0", Assets: []Asset{
		{Name: "dmcode-linux-amd64", URL: srv.URL + "/dl"},
		{Name: ChecksumsName, URL: srv.URL + "/sums"},
	}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "dmcode")
	if err := os.WriteFile(dest, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Install(context.Background(), &Client{API: srv.URL}, rel, dest)
	if err == nil {
		t.Fatal("a mismatched checksum was accepted")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("the error does not name the checksum: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the old binary is gone: %v", err)
	}
	if string(got) != "the old one" {
		t.Errorf("the file holds %q", got)
	}
}

// TestInstallWithoutAChecksumFileStillInstalls: the checksum file was added
// later than the releases themselves, so an older release installs — and says
// that it was not verified.
func TestInstallWithoutAChecksumFileStillInstalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("the binary"))
	}))
	defer srv.Close()

	rel := Release{Tag: "v0.0.1", Assets: []Asset{
		{Name: "dmcode-linux-amd64", URL: srv.URL + "/dl"},
	}}
	dest := filepath.Join(t.TempDir(), "dmcode")
	res, err := Install(context.Background(), &Client{API: srv.URL}, rel, dest)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Verified {
		t.Error("a release with no checksum file was reported as verified")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("the binary is not at the path: %v", err)
	}
}

// TestTheRateLimitIsExplained: a refusal that says "try again later" and one
// that says "there is nothing to install" are different problems, and the
// user has to be able to tell them apart.
func TestTheRateLimitIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := (&Client{API: srv.URL}).Latest(context.Background())
	if err == nil {
		t.Fatal("a refusal was taken for a release")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("the error does not mention the rate limit: %v", err)
	}
}

// TestCompareOrdersVersions: "0.1.10" is newer than "0.1.9", and a string
// comparison says the opposite.
func TestCompareOrdersVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.1.1", "0.1.1", 0},
		{"0.1.10", "0.1.9", 1},
		{"0.1.9", "0.1.10", -1},
		{"v0.2.0", "0.1.9", 1},
		{"0.2.0", "0.2.0-rc1", 1},
		{"0.2", "0.2.0", 0},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
