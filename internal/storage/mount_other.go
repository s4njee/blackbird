//go:build !linux && !darwin

package storage

import "syscall"

func (in *Inspector) mount(_ string, _ *syscall.Statfs_t) string { return "" }
