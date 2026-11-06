//go:build windows

package proctree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Jobs keyed by process id: the shell's Windows process tree is a lie for
// shells that re-parent their children at spawn time — Git Bash launches
// external commands through a helper that exits, so taskkill /T walking the
// parent chain never sees them. A job object does not care about the parent
// chain: whatever was running inside when the shell was assigned dies when
// the job is terminated, and everything the shell spawned afterwards is
// born inside it.
var (
	jobsMu sync.Mutex
	jobs   = map[int]windows.Handle{}
)

// The right AssignProcessToJobObject asks for on the process handle; x/sys
// does not name it, and winnt.h calls it JOB_OBJECT_ASSIGN_PROCESS.
const jobObjectAssignProcess = 0x2000

// Group has nothing to do before the start: there is no process group a
// signal could reach, and the job needs the pid, which only exists after
// the process has started. Attach is the Windows half.
func Group(cmd *exec.Cmd) {}

// Attach puts the started process in a kill-on-close job. A failure here is
// not fatal and is not reported: Kill keeps the taskkill and bare-kill
// fallbacks for exactly the case where no job could be attached.
func Attach(pid int) {
	if pid <= 0 {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	// The race window between the start and this assignment is microseconds,
	// and a shell needs milliseconds to even parse its command line, so
	// children are normally born inside the job. taskkill stays as the
	// fallback for a child that beat the assignment.
	//
	// The process handle is opened for the assign specifically: os.Process's
	// own handle is not exposed, and ConPTY exposes no handle at all, and
	// these are exactly the rights AssignProcessToJobObject asks for.
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|jobObjectAssignProcess, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		windows.CloseHandle(job)
		return
	}
	jobsMu.Lock()
	jobs[pid] = job
	jobsMu.Unlock()
}

// Kill stops the process and everything started under it: terminate the job
// if one is attached (the only route that reaches a re-parented Git Bash
// child), then taskkill /T for anything outside the job, then the bare kill.
func Kill(pid int) error {
	jobsMu.Lock()
	job, ok := jobs[pid]
	jobsMu.Unlock()
	if ok {
		// TerminateJobObject kills every process in the job whatever handles
		// remain open; the close afterwards releases the object so Release
		// does not find it twice.
		_ = windows.TerminateJobObject(job, 1)
	}
	tk := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"),
		"/PID", strconv.Itoa(pid), "/T", "/F")
	tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := tk.Run(); err != nil && !ok {
		p, ferr := os.FindProcess(pid)
		if ferr != nil {
			return ferr
		}
		return p.Kill()
	}
	return nil
}

// Release closes the job handle after the process exited normally. The
// job's contents are gone by then, so the close frees the kernel object
// rather than killing anything.
func Release(pid int) {
	jobsMu.Lock()
	job, ok := jobs[pid]
	delete(jobs, pid)
	jobsMu.Unlock()
	if ok {
		windows.CloseHandle(job)
	}
}
