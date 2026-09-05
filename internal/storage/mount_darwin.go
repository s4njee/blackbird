package storage

import "syscall"

func (in *Inspector) mount(_ string, disk *syscall.Statfs_t) string {
	bytes := make([]byte, 0, len(disk.Mntonname))
	for _, b := range disk.Mntonname {
		if b == 0 {
			break
		}
		bytes = append(bytes, byte(b))
	}
	return string(bytes)
}
