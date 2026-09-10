package fixtures

import "fmt"

type Widget struct {
	Name string
}

func greet(w *Widget) string {
	return fmt.Sprintf("hello %s", w.Name)
}

func (w *Widget) Shout() string {
	return upper(greet(w))
}
