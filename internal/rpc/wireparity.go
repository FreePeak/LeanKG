package rpc

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// ArgsRefusalRecovery wraps the ConnectRPC handler so a codec refusal that
// names the descriptor's internal map VALUE field is rewritten into one that
// names the caller's own argument and this wire's limitation.
//
// It has to be here rather than in the service because the codec failure
// happens while DECODING the request, before any handler — and before any
// interceptor — runs. The service cannot see a request it never received, so
// the only handle on the message is the response the transport writes.
//
// ponytail: it buffers the error body only, and only rewrites a body that
// matches the codec's own wording (see explainArgsRefusal, which passes
// anything else through untouched). A success path is never buffered, and a
// refusal this does not recognise is forwarded byte for byte — so a future
// protobuf costs clarity, never correctness.
func ArgsRefusalRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &errCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status < 400 || rec.body.Len() == 0 {
			_, _ = w.Write(rec.body.Bytes()) // success: forward untouched
			return
		}
		var env struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(rec.body.Bytes(), &env) != nil {
			return // not a ConnectRPC error envelope
		}
		explained := explainArgsRefusal(&connectError{msg: env.Message})
		if explained == nil || explained.Error() == env.Message {
			_, _ = w.Write(rec.body.Bytes()) // not ours: forward untouched
			return
		}
		out, err := json.Marshal(map[string]string{"code": env.Code, "message": explained.Error()})
		if err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.status)
		_, _ = w.Write(out)
	})
}

// errCapture records a handler's response so the refusal can be inspected.
type errCapture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *errCapture) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *errCapture) Write(b []byte) (int, error) {
	return c.body.Write(b)
}

// connectError is the minimal shape explainArgsRefusal needs.
type connectError struct{ msg string }

func (e *connectError) Error() string { return e.msg }
