// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

package shutdown

import (
	"net/http"
)

// WrapHandler will return an http Handler
// That will lock shutdown until all have completed
// and will return http.StatusServiceUnavailable if
// shutdown has been initiated.
func WrapHandler(h http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if !Lock() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// We defer, so panics will not keep a lock
		defer Unlock()
		h.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

// WrapHandlerFunc will return an http.HandlerFunc
// that will lock shutdown until all have completed.
// The handler will return http.StatusServiceUnavailable if
// shutdown has been initiated.
func WrapHandlerFunc(h http.HandlerFunc) http.HandlerFunc {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if !Lock() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// We defer, so panics will not keep a lock
		defer Unlock()
		h(w, r)
	}
	return http.HandlerFunc(fn)
}
