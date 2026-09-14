//go:build !darwin && !linux

package setupcfg

import "os"

// isTerminal falls back to the character-device test on platforms without the
// termios ioctl (Windows): a console handle is a character device, and the
// prompt is only ever a convenience.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
