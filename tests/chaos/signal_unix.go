//go:build !windows

package main

import (
	"os"
	"syscall"
)

func sigTERM() os.Signal { return syscall.SIGTERM }
