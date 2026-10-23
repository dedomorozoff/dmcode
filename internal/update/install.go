package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Result is what an install did.
type Result struct {
	// Version is the release that was installed.
	Version string
	// Path is where the binary now lives.
	Path string
	// Size is how many bytes were downloaded.
	Size int64
	// Checksum is the sha256 that was checked, when there was one.
	Checksum string
	// Verified says whether the download was checked at all. It is false for
	// a release that publishes no checksum file — which is a fact about the
	// release, and not a claim that the download was checked.
	Verified bool
	// Deferred says the running binary was moved aside instead of being
	// overwritten, because the platform will not let a running program be
	// replaced. The new binary is at Path and the old one is still to be
	// removed by hand.
	Deferred bool
}

// Install downloads the release's binary for this platform and puts it at
// dest.
//
// The download is checked against the release's sha256sums.txt when the
// release publishes one, and the binary is written beside dest and renamed
// into place: a half-written file at the path is a broken dmcode, and a rename
// is atomic where a copy is not.
//
// The context bounds the whole thing, so a caller that is told to stop can
// stop: a download that cannot be cancelled is a command the interface cannot
// answer Esc to.
func Install(ctx context.Context, c *Client, r Release, dest string) (Result, error) {
	goos, goarch := Platform()
	asset, ok := r.FindAsset(goos, goarch)
	if !ok {
		return Result{}, fmt.Errorf("the release %s has no build for %s/%s", r.Tag, goos, goarch)
	}
	dir := filepath.Dir(dest)
	tmp, err := os.MkdirTemp(dir, ".dmcode-update-")
	if err != nil {
		return Result{}, fmt.Errorf("cannot write next to %s: %w", dest, err)
	}
	defer os.RemoveAll(tmp)

	bin := filepath.Join(tmp, filepath.Base(dest))
	size, err := c.download(ctx, asset.URL, bin)
	if err != nil {
		return Result{}, err
	}
	sum, verified, err := c.verify(ctx, r, bin, asset.Name)
	if err != nil {
		return Result{}, err
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		return Result{}, fmt.Errorf("cannot make the new binary runnable: %w", err)
	}
	res := Result{Version: r.Version(), Size: size, Checksum: sum, Verified: verified}
	path, deferred, err := replaceFile(bin, dest)
	res.Path = path
	res.Deferred = deferred
	if err != nil {
		return res, err
	}
	return res, nil
}

// download streams a URL into a file and returns how many bytes it wrote.
//
// It is streamed rather than read into memory because the file is a binary of
// tens of megabytes, and a second copy of it in the heap is a second copy the
// machine has to hold.
func (c *Client) download(ctx context.Context, url, dest string) (int64, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the download answered %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return n, fmt.Errorf("the download broke after %d bytes: %w", n, err)
	}
	return n, nil
}

// verify checks the downloaded file against the sha256 the release published.
//
// A release with no checksum file is not a failure: the file was added later
// than the releases themselves, and refusing to install without it would break
// every older release. What must not happen is pretending the file was
// checked — the caller is told, and says so.
func (c *Client) verify(ctx context.Context, r Release, path, name string) (string, bool, error) {
	asset, ok := r.FindAssetByName(ChecksumsName)
	if !ok {
		return "", false, nil
	}
	sums, err := c.get(ctx, asset.URL)
	if err != nil {
		// The checksums are a second file: if it cannot be fetched the
		// install goes on unverified, exactly as install.sh does.
		return "", false, nil
	}
	defer sums.Body.Close()
	body, err := io.ReadAll(io.LimitReader(sums.Body, 1<<20))
	if err != nil {
		return "", false, nil
	}
	want, ok := ChecksumFor(body, name)
	if !ok {
		return "", false, nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return "", false, err
	}
	if !strings.EqualFold(got, want) {
		return want, false, fmt.Errorf("checksum mismatch: the release says %s, the download is %s", want, got)
	}
	return got, true, nil
}

// get makes a request for a file, without reading the body.
func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", AssetPrefix)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// fileSHA256 is the hex sha256 of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// replaceFile puts the downloaded binary where dest is.
//
// On Unix a rename over the running binary is atomic and works: the kernel
// keeps the old inode alive for the process that is running it. Windows will
// not overwrite a running executable, but it will rename one, so the old
// binary is moved aside and the new one takes its place — and the file that
// was moved aside cannot be deleted until the program exits, so the next
// start cleans it up.
func replaceFile(tmp, dest string) (path string, deferred bool, err error) {
	if runtime.GOOS != "windows" {
		if err := os.Rename(tmp, dest); err != nil {
			return "", false, fmt.Errorf("cannot put the new binary at %s: %w", dest, err)
		}
		return dest, false, nil
	}
	old := dest + ".old"
	_ = os.Remove(old)
	if err := os.Rename(dest, old); err != nil && !os.IsNotExist(err) {
		return "", false, fmt.Errorf("cannot move the running binary aside: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		// The old one goes back: an empty path would break the next start as
		// well as this one.
		_ = os.Rename(old, dest)
		return "", false, fmt.Errorf("cannot put the new binary at %s: %w", dest, err)
	}
	return dest, true, nil
}

// CleanupStale removes what an earlier update left behind.
//
// On Windows the binary that was running could be renamed but not deleted, so
// the next start finds it beside the new one. It is removed here rather than
// by the update that left it: a program cannot delete the file it is running
// from.
func CleanupStale(exePath string) {
	if exePath == "" {
		return
	}
	_ = os.Remove(exePath + ".old")
	dir := filepath.Dir(exePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".dmcode-update-") {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
