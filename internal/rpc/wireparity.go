package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// The generated descriptor types `args` as map<string,string>: a protobuf
// comment on the field even says clients "may send typed values over other
// transports", which is true and is also the whole problem. Over this wire
// every NON-STRING action argument is unreachable — `limit`, `depth`,
// `insert_line`, `retained_through_user_turn`, and the array `turns`.
//
// The descriptor is generated code and the repository keeps no .proto source,
// so retyping the map is a schema change with no regeneration story — a product
// decision, not a patch. What IS a patch is the legibility: the codec refuses
// with
//
//	unmarshal message: … proto: (line 1:48): invalid value for string field value: 2
//
// which names `value` — a descriptor field the caller never sent — and not the
// argument they did. A confident client reading a message about its own request
// that cannot be about its own request concludes the request was malformed and
// retries it identically. So the refusal is translated to name what the caller
// can act on.
//
// ponytail: a regexp against the codec's wording, which is the only handle on
// the offending argument (it reports a byte offset, not a field path). It
// degrades to passing the original message through untouched, so a future
// protobuf whose wording changes costs clarity and never correctness.
var codecArgRefusal = regexp.MustCompile(`invalid value for string field value: (.+)$`)

// argsWireNote explains the wire's own limitation, in the caller's terms.
const argsWireNote = "this ConnectRPC descriptor types args as map<string,string>, so only STRING action args reach it " +
	"(over MCP the same call takes any JSON value); send it as a string here, or use the MCP or REST surface for typed args"

// explainArgsRefusal rewrites a codec refusal that names the internal map VALUE
// field into one that names the caller's own argument and the wire's limit.
// Anything it does not recognise is returned unchanged.
func explainArgsRefusal(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.Contains(msg, "string field value") {
		return err
	}
	got := codecArgRefusal.FindStringSubmatch(msg)
	if got == nil {
		// The wording changed: pass the original through rather than guess.
		return err
	}
	// The codec quotes the rejected value (e.g. `2` or `["a","b"]`) but not the
	// KEY, so name what the caller can act on — the value they sent and the
	// wire's limit — rather than invent a key. The codec's own wording is kept
	// in parentheses so nothing is lost.
	value := strings.TrimSpace(got[1])
	return fmt.Errorf("args value %s is not a string and this wire cannot carry it: %s (%v)",
		value, argsWireNote, err)
}

// ArgsRefusalRecovery wraps a ConnectRPC handler so a codec refusal is rewritten
// where the caller can see it.
//
// It has to wrap the handler rather than sit inside the service: the codec
// failure happens while DECODING the request, before any handler — and before
// any interceptor — runs. The service cannot see a request it never received, so
// the only handle on the message is the response the transport writes.
//
// ponytail: it buffers the error body only, and only rewrites a body matching
// the codec's own wording. A success path is forwarded byte for byte, and a
// refusal this does not recognise is forwarded byte for byte.
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

// errCapture records a handler's response so the refusal can be inspected
// without ever buffering a success body the caller is waiting on.
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
