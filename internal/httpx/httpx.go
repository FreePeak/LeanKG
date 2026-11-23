// Package httpx holds the request-lifetime conventions and the response
// writers shared by the HTTP transports: a request whose client has gone away
// is answered without doing its work and is not a data failure, and every
// failed call answers with the status the one error table gives it.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// MaxBodyBytes bounds a JSON request body on every HTTP decode path. A body
// over it answers 413 RequestTooLarge instead of being read to the end.
const MaxBodyBytes = 1 << 20

// StatusClientClosedRequest is nginx's 499: the client closed the connection
// before the answer was written. net/http has no constant for it.
const StatusClientClosedRequest = 499

// codeStatus is the one error-code table: the status a catalog code answers
// with. A code with no entry here is a bad request (400): the client sent
// something the server cannot use (unknown action, missing or invalid param,
// a path outside the project). Every other class answers with its own status.
var codeStatus = map[string]int{
	errs.Unauthorized.Code:     http.StatusUnauthorized,
	errs.PermissionDenied.Code: http.StatusForbidden,
	errs.ReadOnly.Code:         http.StatusForbidden,

	errs.ProjectNotInitialized.Code: http.StatusNotFound,
	errs.UnknownProject.Code:        http.StatusNotFound,
	errs.UnknownTool.Code:           http.StatusNotFound,
	errs.MethodNotFound.Code:        http.StatusNotFound,

	errs.NoVectors.Code:         http.StatusConflict,
	errs.IndexRootMismatch.Code: http.StatusConflict,

	errs.PGUnreachable.Code:    http.StatusServiceUnavailable,
	errs.TrgmUnavailable.Code:  http.StatusServiceUnavailable,
	errs.IndexUnavailable.Code: http.StatusServiceUnavailable,

	errs.RequestTooLarge.Code: http.StatusRequestEntityTooLarge,

	errs.PGURLMalformed.Code:   http.StatusInternalServerError,
	errs.AutoAttachFailed.Code: http.StatusInternalServerError,
}

// StatusFor returns the HTTP status a failed call answers with: 499 for a
// cancelled call, the table's status for a catalog error, 500 for a store
// failure, 400 otherwise.
func StatusFor(err error) int {
	if errors.Is(err, context.Canceled) {
		return StatusClientClosedRequest
	}
	if code := CodeOf(err); code != "" {
		if status, ok := codeStatus[code]; ok {
			return status
		}
	}
	var storeErr *store.Error
	if errors.As(err, &storeErr) {
		return http.StatusInternalServerError
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

// bodyError is a request body the server refused. Its message is the plain
// cause, the text clients parsed before the catalog reached the body; the
// catalog code still travels in "code" and picks the status.
type bodyError struct{ coded *errs.Error }

func (e bodyError) Error() string { return e.coded.Cause }

func (e bodyError) Unwrap() error { return e.coded }

// InvalidBody is the error for a request body that does not decode as JSON.
// It carries the InvalidParam code, so every typed-body decode failure answers
// with the same 400 and token as a bad query field.
func InvalidBody(err error) error {
	return bodyError{errs.NewError(errs.InvalidParam, "invalid JSON body: "+err.Error(), "")}
}

// DecodeJSON reads the request body into into, capped at MaxBodyBytes. A body
// over the cap is RequestTooLarge; a body that does not decode is InvalidBody.
func DecodeJSON(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	err := json.NewDecoder(r.Body).Decode(into)
	if err == nil {
		return nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return bodyError{errs.NewError(errs.RequestTooLarge, errs.RequestTooLarge.Cause, "")}
	}
	return InvalidBody(err)
}

// WriteJSON encodes v as the JSON body of a response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Message is the "error" text a client sees for err. A client error keeps its
// own text: a refused body its plain cause, any other error its full catalog
// rendering. A server fault (5xx) carries no cause text, because driver errors
// name tables, columns and hosts: it answers the catalog's static message for
// its code, or a generic line, and the cause goes to the log.
func Message(err error) string {
	var body bodyError
	if errors.As(err, &body) {
		return body.Error()
	}
	if StatusFor(err) < http.StatusInternalServerError {
		return err.Error()
	}
	log.Printf("httpx: server fault: %v", err)
	var coded *errs.Error
	if errors.As(err, &coded) {
		return coded.Entry.Message()
	}
	return "internal server error"
}

// WriteError answers a failed call: the status StatusFor gives err and the
// body {"error": Message(err)}, plus "code" when err is a catalog error so
// clients branch on the stable token (FR-ZCP-12). A cancelled call has no
// body: the client that would read it is gone.
func WriteError(w http.ResponseWriter, err error) {
	writeError(w, err, StatusFor(err))
}

// WriteErrorAs is WriteError for a transport whose client errors share one
// status, such as the auth envelope's 400. A server fault (5xx) keeps StatusFor
// and the generic Message; any other failure answers clientStatus.
func WriteErrorAs(w http.ResponseWriter, clientStatus int, err error) {
	status := StatusFor(err)
	if status < http.StatusInternalServerError {
		status = clientStatus
	}
	writeError(w, err, status)
}

func writeError(w http.ResponseWriter, err error, status int) {
	if errors.Is(err, context.Canceled) {
		w.WriteHeader(StatusClientClosedRequest)
		return
	}
	body := map[string]any{"error": Message(err)}
	if code := CodeOf(err); code != "" {
		body["code"] = code
	}
	WriteJSON(w, status, body)
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
