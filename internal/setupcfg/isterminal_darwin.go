//go:build darwin

package setupcfg

import "syscall"

// termiosReq is the Darwin termios read request.
const termiosReq = uintptr(syscall.TIOCGETA)
