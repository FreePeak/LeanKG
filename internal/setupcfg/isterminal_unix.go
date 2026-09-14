//go:build darwin || linux

package setupcfg

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal, via the termios read ioctl
// (Rust: std::io::IsTerminal). An unsupported request just reports false —
// the prompt is only ever a convenience.
func isTerminal(f *os.File) bool {
	return termiosOK(f, termiosReq)
}

func termiosOK(f *os.File, request uintptr) bool {
	if f == nil {
		return false
	}
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, f.Fd(),
		request, uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}
