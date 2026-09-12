//go:build linux

package setupcfg

import "syscall"

// termiosReq is the Linux termios read request.
const termiosReq = uintptr(syscall.TCGETS)
