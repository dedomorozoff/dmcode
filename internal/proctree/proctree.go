// Package proctree makes one process killable as a tree.
//
// A shell is killed all the time; what it spawned almost never is. Killing
// only the shell is the bug this package exists to prevent: a cancelled
// `go build` kept compiling behind a dead bash, holding the output pipes,
// and whoever waited on those pipes waited for the build's own schedule, not
// for the cancellation.
//
// Two mechanisms, chosen because the naive one is broken on the platforms
// that matter:
//
//   - Unix puts the shell in a process group of its own (`Setpgid`) and the
//     kill signals the group. No tree walking, nothing forks between the walk
//     and the kill.
//   - Windows has no process groups, and walking the parent chain is a lie:
//     Git Bash and Cygwin re-parent external commands at spawn time, so the
//     grandchildren are not under the shell by the time anyone looks. A
//     kill-on-close job object does not care about the parent chain — what
//     was running inside when the shell was assigned dies with it, and
//     everything the shell spawns afterwards is born inside.
//
// The API is pid-shaped rather than *exec.Cmd-shaped because the two users
// have different things to hold: internal/tools has a command it is waiting
// on, and the editor's PTY has a pid that ConPTY started for it.
package proctree
