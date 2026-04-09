//go:build !windows

package launch

import (
	"os"
	"syscall"
)

func shellWindowChangeSignal() os.Signal {
	return syscall.SIGWINCH
}
