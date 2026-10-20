package dashboard

import (
	"errors"
	"testing"
)

func TestBrowserCommandPerPlatform(t *testing.T) {
	env := func(map[string]string) func(string) string {
		return func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] }
	}
	none := func(string) string { return "" }

	name, args, err := browserCommand("darwin", "http://127.0.0.1:9701/", none)
	if err != nil || name != "open" || len(args) != 1 || args[0] != "http://127.0.0.1:9701/" {
		t.Errorf("darwin = %q %v %v", name, args, err)
	}
	name, args, err = browserCommand("linux", "http://u/", env(nil))
	if err != nil || name != "xdg-open" || args[len(args)-1] != "http://u/" {
		t.Errorf("linux with display = %q %v %v", name, args, err)
	}
	if _, _, err := browserCommand("linux", "http://u/", none); !errors.Is(err, ErrNoDisplay) {
		t.Errorf("linux without display: err = %v, want ErrNoDisplay", err)
	}
	name, args, err = browserCommand("windows", "http://u/", none)
	if err != nil || name != "rundll32" || args[0] != "url.dll,FileProtocolHandler" || args[1] != "http://u/" {
		t.Errorf("windows = %q %v %v", name, args, err)
	}
	if _, _, err := browserCommand("plan9", "http://u/", none); err == nil {
		t.Errorf("unsupported platform accepted")
	}
}
