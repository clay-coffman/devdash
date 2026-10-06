package server

import (
	"os"
	"syscall"

	"github.com/clay-coffman/devdash/internal/collect"
)

func kill(pid int, sig syscall.Signal) error {
	return collect.Kill(pid, os.Getuid(), sig)
}
