// +build ignore

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/klauspost/shutdown2"
)

// This example shows a server that has message processing in a separate goroutine
//
// When the server is shut down (via ctrl+c for instance), it will notify an upstream
// server, and all new incoming requests will get a 'StatusServiceUnavailable' (503)
// response code.
//
// The server will finish all pending requests before shutdown is initiated,
// so all requests are handled gracefully.
//
// To execute, use 'go run server-example.go'
//
// Open the server at http://localhost:8080
// To shut down the server, go to http://localhost:8080/?shutdown=true or press ctrl+c

// A Sample Webserver
func HelloServer(w http.ResponseWriter, req *http.Request) {
	// Tracks all running requests
	l := shutdown.Lock()
	if l != nil {
		defer l()
	} else {
		// Shutdown has started, return that the service is unavailable
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Server is now shutting down"))
		return
	}

	if req.FormValue("shutdown") != "" {
		request <- "shutdown"
		log.Println("Requesting server shutdown")
		// We start the exit in a separate go-routine, otherwise this request will have
		// to wait for shutdown to be completed.
		go shutdown.Exit(0)
	} else {
		// Add artificial delay
		time.Sleep(time.Second * 5)
		request <- "greet"
	}
	io.WriteString(w, <-reply)
}

func main() {
	// Make shutdown catch Ctrl+c and system terminate
	shutdown.OnSignal(0, os.Interrupt, syscall.SIGTERM)

	// In the first stage we will make sure all request have finished
	shutdown.FirstFn(func() {
		log.Println("Notify upstream we are going offline")
		// TODO: Send a request upstream
	})

	// Start a service
	go dataLoop()

	// Start a webserver
	http.HandleFunc("/", HelloServer)
	fmt.Println("Starting server. Press Ctrl+c to initiate shutdown,\nor go to http://localhost:8080/?shutdown=true")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

var request = make(chan string)
var reply = make(chan string)

func dataLoop() {
	// We register for Second stage shutdown notification,
	// since we don't want to stop this service while requests are still being handled.
	end := shutdown.Second()
	for {
		select {
		case v := <-request:
			if v == "greet" {
				reply <- "hello world\n"
			} else if v == "shutdown" {
				reply <- "initiating server shutdown\n"
			} else {
				reply <- "unknown command\n"
			}
		case n := <-end:
			log.Println("Exiting data loop")
			close(request)
			close(reply)
			close(n)
			return
		}
	}
}
