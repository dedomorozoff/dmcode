package mcp

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadMergesWorkspaceOverHome(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and everywhere else

	homeDir := filepath.Join(home, ".dmcode")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "mcp.json"),
		[]byte(`{"mcpServers":{"fs":{"command":"npx"},"shared":{"command":"home-version"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".mcp.json"),
		[]byte(`{"mcpServers":{"shared":{"url":"https://project.example/mcp"},"remote":{"url":"https://remote.example/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, notes := Load(work)
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	if got := servers["fs"].Command; got != "npx" {
		t.Errorf("fs.command = %q, want the home config's server", got)
	}
	if got := servers["shared"].URL; got != "https://project.example/mcp" {
		t.Errorf("shared.url = %q, want the workspace file to win on a name collision", got)
	}
	if _, ok := servers["remote"]; !ok {
		t.Errorf("remote is missing, want both files merged")
	}
}

func TestLoadWithoutFiles(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	servers, notes := Load(work)
	if len(servers) != 0 || len(notes) != 0 {
		t.Errorf("Load with no config files = %v, %v; want empty, empty", servers, notes)
	}
}

func TestLoadReportsBadJSONAndIncompleteServers(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	if err := os.WriteFile(filepath.Join(work, ".mcp.json"),
		[]byte(`{"mcpServers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	homeDir := filepath.Join(home, ".dmcode")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "mcp.json"),
		[]byte(`{"mcpServers":{"empty":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, notes := Load(work)
	if len(servers) != 0 {
		t.Errorf("servers = %v, want the unusable ones dropped", servers)
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v, want one per problem", notes)
	}
}

func TestToolsetsReturnsNilWithoutServers(t *testing.T) {
	if ts := Toolsets(nil); ts != nil {
		t.Errorf("Toolsets(nil) = %v, want nil", ts)
	}
	if ts := Toolsets(map[string]Server{"empty": {}}); ts != nil {
		t.Errorf("Toolsets of an unusable server = %v, want nil", ts)
	}
}

func TestSortedNamesIsStable(t *testing.T) {
	names := sortedNames(map[string]Server{"b": {Command: "x"}, "a": {Command: "y"}})
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Errorf("sortedNames = %v, want [a b]", names)
	}
}
