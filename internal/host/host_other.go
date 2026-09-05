//go:build !linux && !darwin

package host

// Unsupported platforms report nothing; the UI renders dashes.
func readLinuxLoad(st *Stats)  {}
func readLinuxMem(st *Stats)   {}
func readLinuxSelf(st *Stats)  {}
func readDarwinLoad(st *Stats) {}
func readDarwinMem(st *Stats)  {}
