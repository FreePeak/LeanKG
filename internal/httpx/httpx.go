// Package httpx holds the request-lifetime conventions and the response
// writers shared by the HTTP transports: a request whose client has gone away
// is answered without doing its work and is not a data failure, and every
// failed call answers with the status the one error table gives it.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/FreePeak/LeanKG/internal/errs"
)

// StatusClientClosedRequest is nginx's 499: the client closed the connection
// before the answer was written. net/http has no constant for it.
const StatusClientClosedRequest = 499

// codeStatus is the one error-code table: the status a catalog code answers
// with. An error with no entry here, catalog or not, is a bad request (400),
// which is what the REST layer has always answered.
var codeStatus = map[string]int{
	errs.Unauthorized.Code:     http.StatusUnauthorized,
	errs.PermissionDenied.Code: http.StatusForbidden,
}

// StatusFor returns the HTTP status a failed call answers with: 499 for a
// cancelled call, the table's status for a catalog error, 400 otherwise.
func StatusFor(err error) int {
	if errors.Is(err, context.Canceled) {
		return StatusClientClosedRequest
	}
	if code := CodeOf(err); code != "" {
		if status, ok := codeStatus[code]; ok {
			return status
		}
	}
	return http.StatusBadRequest
}

// CodeOf returns the stable catalog code of err, or "" when err carries none.
func CodeOf(err error) string {
	var coded *errs.Error
	if errors.As(err, &coded) {
		return coded.Code()
	}
	return ""
}

// WriteJSON encodes v as the JSON body of a response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError answers a failed call: the status StatusFor gives err and the
// body {"error": message}, plus "code" when err is a catalog error so clients
// branch on the stable token (FR-ZCP-12). A cancelled call has no body: the
// client that would read it is gone.
func WriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		w.WriteHeader(StatusClientClosedRequest)
		return
	}
	body := map[string]any{"error": err.Error()}
	if code := CodeOf(err); code != "" {
		body["code"] = code
	}
	WriteJSON(w, StatusFor(err), body)
}

// WriteMessage answers with an explicit status and the body {"error": msg}.
// It is for the call sites that know their class and have no error value.
func WriteMessage(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// Guard returns h unless the request's context is already done, in which case
// it answers StatusClientClosedRequest without calling h.
func Guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			w.WriteHeader(StatusClientClosedRequest)
			return
		}
		h.ServeHTTP(w, r)
	})
}
