package poller

import "syscall"

// statVolumes snapshots free/total space for each configured mount path via
// statfs. Unreadable paths are skipped (they surface as missing rows rather
// than breaking the poll cycle).
func statVolumes(paths []string) []Volume {
	out := make([]Volume, 0, len(paths))
	for _, p := range paths {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			continue
		}
		bsize := uint64(st.Bsize)
		out = append(out, Volume{
			Path:       p,
			TotalBytes: st.Blocks * bsize,
			FreeBytes:  st.Bavail * bsize,
		})
	}
	return out
}
