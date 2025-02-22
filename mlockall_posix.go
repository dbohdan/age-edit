//go:build aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func lockMemory() error {
	if err := unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE); err != nil {
		return fmt.Errorf("failed to lock memory: %v", err)
	}

	return nil
}
