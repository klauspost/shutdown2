// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

package shutdown

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"
)

// This example creates a custom function handler
// and wraps the handler, so all request will
// finish before shutdown is started.
//
// If requests take too long to finish (see the shutdown will proceed
// and clients will be disconnected when the server shuts down.
// To modify the timeout use SetTimeoutN(Preshutdown, duration)
func ExampleWrapHandlerFunc() {
	// Set a custom timeout, if the 5 second default doesn't fit your needs.
	SetTimeoutN(Preshutdown, time.Second*30)
	// Catch OS signals
	OnSignal(0, os.Interrupt, syscall.SIGTERM)

	// Example handler function
	fn := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, %q", html.EscapeString(r.URL.Path))
	}

	// Wrap the handler function
	http.HandleFunc("/", WrapHandlerFunc(fn))

	// Start the server
	http.ListenAndServe(":8080", nil)
}

// This example creates a fileserver
// and wraps the handler, so all request will
// finish before shutdown is started.
//
// If requests take too long to finish the shutdown will proceed
// and clients will be disconnected when the server shuts down.
// To modify the timeout use SetTimeoutN(Preshutdown, duration)
func ExampleWrapHandler() {
	// Set a custom timeout, if the 5 second default doesn't fit your needs.
	SetTimeoutN(Preshutdown, time.Second*30)
	// Catch OS signals
	OnSignal(0, os.Interrupt, syscall.SIGTERM)

	// Create a fileserver handler
	fh := http.FileServer(http.Dir("/examples"))

	// Wrap the handler function
	http.Handle("/", WrapHandler(fh))

	// Start the server
	http.ListenAndServe(":8080", nil)
}

func TestWrapHandlerBasic(t *testing.T) {
	reset()
	defer close(startTimer(t))
	var finished = false
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finished = true
	})

	wrapped := WrapHandler(fn)
	res := httptest.NewRecorder()
	req, _ := http.NewRequest("", "", bytes.NewBufferString(""))
	wrapped.ServeHTTP(res, req)
	if res.Code == http.StatusServiceUnavailable {
		t.Fatal("Expected result code NOT to be", http.StatusServiceUnavailable, "got", res.Code)
	}
	if !finished {
		t.Fatal("Handler was not executed")
	}

	Shutdown()
	finished = false
	res = httptest.NewRecorder()
	wrapped.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatal("Expected result code to be", http.StatusServiceUnavailable, " got", res.Code)
	}
	if finished {
		t.Fatal("Unexpected execution of funtion")
	}
}

func TestWrapHandlerFuncBasic(t *testing.T) {
	reset()
	defer close(startTimer(t))
	var finished = false
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finished = true
	})

	wrapped := WrapHandlerFunc(fn)
	res := httptest.NewRecorder()
	req, _ := http.NewRequest("", "", bytes.NewBufferString(""))
	wrapped(res, req)
	if res.Code == http.StatusServiceUnavailable {
		t.Fatal("Expected result code NOT to be", http.StatusServiceUnavailable, "got", res.Code)
	}
	if !finished {
		t.Fatal("Handler was not executed")
	}

	Shutdown()
	finished = false
	res = httptest.NewRecorder()
	wrapped(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatal("Expected result code to be", http.StatusServiceUnavailable, " got", res.Code)
	}
	if finished {
		t.Fatal("Unexpected execution of funtion")
	}
}

// Test if panics locks shutdown.
func TestWrapHandlerPanic(t *testing.T) {
	reset()
	SetTimeout(time.Second)
	defer close(startTimer(t))
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	wrapped := WrapHandler(fn)
	res := httptest.NewRecorder()
	req, _ := http.NewRequest("", "", bytes.NewBufferString(""))
	func() {
		defer func() {
			recover()
		}()
		wrapped.ServeHTTP(res, req)
	}()

	// There should be no locks held, so it should finish immediately
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Millisecond*500 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
}

// Test if panics locks shutdown.
func TestWrapHandlerFuncPanic(t *testing.T) {
	reset()
	SetTimeout(time.Millisecond * 200)
	defer close(startTimer(t))
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	wrapped := WrapHandlerFunc(fn)
	res := httptest.NewRecorder()
	req, _ := http.NewRequest("", "", bytes.NewBufferString(""))
	func() {
		defer func() {
			recover()
		}()
		wrapped(res, req)
	}()

	// There should be no locks held, so it should finish immediately
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Millisecond*100 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
}

// Tests that shutdown doesn't complete until handler function has returned
func TestWrapHandlerOrder(t *testing.T) {
	reset()
	defer close(startTimer(t))
	var finished = make(chan bool)
	var wait = make(chan bool)
	var waiting = make(chan bool)
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(waiting)
		<-wait
	})

	wrapped := WrapHandler(fn)

	go func() {
		res := httptest.NewRecorder()
		req, _ := http.NewRequest("", "", bytes.NewBufferString(""))
		wrapped.ServeHTTP(res, req)
		close(finished)
	}()

	release := time.After(time.Millisecond * 100)
	completed := make(chan bool)
	testOK := make(chan bool)
	go func() {
		select {
		case <-release:
			select {
			case <-finished:
				panic("Shutdown was already finished")
			case <-completed:
				panic("Shutdown had already completed")
			default:
			}
			close(wait)
			close(testOK)
		}
	}()
	<-waiting
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Millisecond*400 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
	close(completed)
	// We should make sure the release has run before exiting
	<-testOK

	select {
	case <-finished:
	default:
		t.Fatal("Function had not finished")
	}
}

// Tests that shutdown doesn't complete until handler function has returned
func TestWrapHandlerFuncOrder(t *testing.T) {
	reset()
	defer close(startTimer(t))
	var finished = make(chan bool)
	var wait = make(chan bool)
	var waiting = make(chan bool)
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(waiting)
		<-wait
	})

	wrapped := WrapHandlerFunc(fn)

	go func() {
		res := httptest.NewRecorder()
		req, _ := http.NewRequest("", "", bytes.NewBufferString(""))
		wrapped(res, req)
		close(finished)
	}()

	release := time.After(time.Millisecond * 100)
	completed := make(chan bool)
	testOK := make(chan bool)
	go func() {
		select {
		case <-release:
			select {
			case <-finished:
				panic("Shutdown was already finished")
			case <-completed:
				panic("Shutdown had already completed")
			default:
			}
			close(wait)
			close(testOK)
		}
	}()
	<-waiting
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Millisecond*400 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
	close(completed)
	// We should make sure the release has run before exiting
	<-testOK

	select {
	case <-finished:
	default:
		t.Fatal("Function had not finished")
	}
}
