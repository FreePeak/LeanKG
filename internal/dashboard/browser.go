package dashboard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// ErrNoDisplay means the platform has no graphical session to open a browser in.
var ErrNoDisplay = errors.New("no display: cannot open a browser")

// OpenBrowser starts the platform browser on url and does not wait for it.
func OpenBrowser(url string) error {
	name, args, err := browserCommand(runtime.GOOS, url, os.Getenv)
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// browserCommand is the opener for goos: open on darwin, xdg-open on linux
// (only with DISPLAY or WAYLAND_DISPLAY set), rundll32 on windows.
func browserCommand(goos, url string, getenv func(string) string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "linux":
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return "", nil, ErrNoDisplay
		}
		return "xdg-open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	}
	return "", nil, fmt.Errorf("no browser opener for %s", goos)
}
