//go:build windows

package launch

import "os"

func shellWindowChangeSignal() os.Signal {
	return nil
}
