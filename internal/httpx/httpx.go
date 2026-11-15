// Package httpx holds the request-lifetime conventions shared by the HTTP
// transports: a request whose client has gone away is answered without doing
// its work, and that answer is not a data failure.
package httpx

import "net/http"

// StatusClientClosedRequest is nginx's 499: the client closed the connection
// before the answer was written. net/http has no constant for it.
const StatusClientClosedRequest = 499

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
