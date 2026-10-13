package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDbgMtime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(p, []byte("one\n"), 0o644)
	b := takeSnapshot(dir)
	time.Sleep(2 * time.Millisecond)
	_ = os.WriteFile(p, []byte("two\n"), 0o644) // same size!
	a := takeSnapshot(dir)
	fmt.Println("before:", b.files[p], "after:", a.files[p])
	fmt.Println("equal modtime:", b.files[p].modTime.Equal(a.files[p].modTime))
	fmt.Println("edited:", snapshotEdited(b, a))
}
