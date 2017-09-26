// Copyright (c) 2017 Klaus Post, released under MIT License. See LICENSE file.

// +build go1.8

package shutdown

import (
	"context"
	"net/http"
	"time"
)

// GracefulHTTP will attempt to gracefully shut down the HTTP server.
// A timeout of 50ms less than Pre Shutdown stage timeout is given to the context.
func GracefulHTTP(s *http.Server) {
	PreShutdownFn(func() {
		// Give the server a little less time.
		srM.RLock()
		to := timeouts[0] - 50*time.Millisecond
		srM.RUnlock()
		ctx, cancel := context.WithTimeout(context.Background(), to)
		defer cancel()
		err := s.Shutdown(ctx)
		if err != nil {
			LoggerMu.Lock()
			Logger.Printf(ErrorPrefix+" HTTP server shutdown returned: %v", err)
			LoggerMu.Unlock()
		}
	})
}

// GracefulListenAndServe will do a ListenAndServe on the provided handler.
// On shutdown it will do a graceful shutdown of the server at pre-shutdown.
// This function blocks until shutdown has started on success.
// If the server is unable to ListenAndServe the error is returned.
// Parameters are the same as http.ListenAndServe().
func GracefulListenAndServe(addr string, handler http.Handler) error {
	hs := &http.Server{Addr: addr, Handler: handler}
	GracefulHTTP(hs)
	err := hs.ListenAndServe()
	if err != http.ErrServerClosed {
		return err
	}
	return nil
}

// GracefulListenAndServeTLS will do a ListenAndServeTLS on the provided handler.
// On shutdown it will do a graceful shutdown of the server at pre-shutdown.
// This function blocks until shutdown has started on success.
// If the server is unable to ListenAndServe the error is returned.
// Parameters are the same as http.ListenAndServeTLS().
func GracefulListenAndServeTLS(addr, certFile, keyFile string, handler http.Handler) error {
	hs := &http.Server{Addr: addr, Handler: handler}
	GracefulHTTP(hs)
	err := hs.ListenAndServeTLS(certFile, keyFile)
	if err != http.ErrServerClosed {
		return err
	}
	return nil
}
