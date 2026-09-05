package storage

import (
	"io"
	"os"
	"syscall"
)

func (in *Inspector) mount(path string, _ *syscall.Statfs_t) string {
	if !in.mountsLoaded {
		in.mountsLoaded = true
		file, err := os.Open("/proc/self/mountinfo")
		if err != nil {
			return ""
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 1<<20))
		if err != nil || len(data) >= 1<<20 {
			return ""
		}
		in.mounts = parseMounts(string(data))
	}
	return mountFor(path, in.mounts)
}
