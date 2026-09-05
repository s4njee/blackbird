//go:build unix

package unpack

import "syscall"

// reniceProcess lowers a child process's scheduling priority (best effort;
// failures are ignored by the caller). Extraction is background work and must
// not starve the daemon or the console.
func reniceProcess(pid int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10)
}
