// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

// Package shutdown provides management of your shutdown process.
//
// The package will enable you to get notifications for your application and handle the shutdown process.
//
// See more information about the how to use it in the README.md file
//
// Package home: https://github.com/klauspost/shutdown2
package shutdown

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"time"
	"runtime"
	"fmt"
)

// Stage contains stage information.
// Valid values for this is exported as variables StageN.
type Stage struct {
	n int
}

var (
	// Logger used for output.
	// This can be exchanged with your own.
	Logger = log.New(os.Stderr, "[shutdown]: ", log.LstdFlags)

	// StagePS indicates the pre shutdown stage when waiting for locks to be released.
	StagePS = Stage{0}

	// Stage1 Indicates first stage of timeouts.
	Stage1 = Stage{1}

	// Stage2 Indicates second stage of timeouts.
	Stage2 = Stage{2}

	// Stage3 indicates third stage of timeouts.
	Stage3 = Stage{3}
)

// Notifier is a channel, that will be sent a channel
// once the application shuts down.
// When you have performed your shutdown actions close the channel you are given.
type Notifier chan chan struct{}

type fnNotify struct {
	client   Notifier
	internal Notifier
	cancel   chan struct{}
}

var sqM sync.Mutex // Mutex for below
var shutdownQueue [4][]Notifier
var shutdownFnQueue [4][]fnNotify
var shutdownFinished = make(chan struct{}, 0) // Closed when shutdown has finished

var srM sync.RWMutex // Mutex for below
var shutdownRequested = false
var timeouts = [4]time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second}

// SetTimeout sets maximum delay to wait for each stage to finish.
// When the timeout has expired for a stage the next stage will be initiated.
func SetTimeout(d time.Duration) {
	srM.Lock()
	for i := range timeouts {
		timeouts[i] = d
	}
	srM.Unlock()
}

// SetTimeoutN set maximum delay to wait for a specific stage to finish.
// When the timeout expired for a stage the next stage will be initiated.
// The stage can be obtained by using the exported variables called 'Stage1, etc.
func SetTimeoutN(s Stage, d time.Duration) {
	srM.Lock()
	timeouts[s.n] = d
	srM.Unlock()
}

// Cancel a Notifier.
// This will remove a notifier from the shutdown queue,
// and it will not be signalled when shutdown starts.
// If the shutdown has already started this will not have any effect.
func (s *Notifier) Cancel() {
	srM.RLock()
	if shutdownRequested {
		srM.RUnlock()
		return
	}
	srM.RUnlock()
	sqM.Lock()
	var a chan chan struct{}
	var b chan chan struct{}
	a = *s
	for n, sdq := range shutdownQueue {
		for i, qi := range sdq {
			b = qi
			if a == b {
				shutdownQueue[n] = append(shutdownQueue[n][:i], shutdownQueue[n][i+1:]...)
			}
		}
		for i, fn := range shutdownFnQueue[n] {
			b = fn.client
			if a == b {
				// Find the matching internal and remove that.
				for i := range shutdownQueue[n] {
					b = shutdownQueue[n][i]
					if fn.internal == b {
						shutdownQueue[n] = append(shutdownQueue[n][:i], shutdownQueue[n][i+1:]...)
					}
				}
				// Cancel, so the goroutine exits.
				close(fn.cancel)
				// Remove this
				shutdownFnQueue[n] = append(shutdownFnQueue[n][:i], shutdownFnQueue[n][i+1:]...)
			}
		}
	}
	sqM.Unlock()
}

// PreShutdown will return a Notifier that will be fired as soon as the shutdown
// is signalled, before locks are released.
// This allows to for instance send signals to upstream servers not to send more requests.
func PreShutdown() Notifier {
	return onShutdown(0)
}

// PreShutdownFn registers a function that will be called as soon as the shutdown
// is signalled, before locks are released.
// This allows to for instance send signals to upstream servers not to send more requests.
func PreShutdownFn(fn func()) Notifier {
	return onFunc(0, fn)
}

// First returns a notifier that will be called in the first stage of shutdowns
func First() Notifier {
	return onShutdown(1)
}

// FirstFn executes a function in the first stage of the shutdown
func FirstFn(fn func()) Notifier {
	return onFunc(1, fn)
}

// Second returns a notifier that will be called in the second stage of shutdowns
func Second() Notifier {
	return onShutdown(2)
}

// SecondFn executes a function in the second stage of the shutdown
func SecondFn(fn func()) Notifier {
	return onFunc(2, fn)
}

// Third returns a notifier that will be called in the third stage of shutdowns
func Third() Notifier {
	return onShutdown(3)
}

// ThirdFn executes a function in the third stage of the shutdown
// The returned Notifier is only really useful for cancelling the shutdown function
func ThirdFn(fn func()) Notifier {
	return onFunc(3, fn)
}

// Create a function notifier.
func onFunc(prio int, fn func()) Notifier {
	f := fnNotify{
		internal: onShutdown(prio),
		cancel:   make(chan struct{}),
		client:   make(Notifier, 1),
	}
	go func() {
		select {
		case <-f.cancel:
			return
		case c := <-f.internal:
			{
				defer func() {
					if r := recover(); r != nil {
						Logger.Println("Panic in shutdown function:", r)
					}
					if c != nil {
						close(c)
					}
				}()
				fn()
			}
		}
	}()
	sqM.Lock()
	shutdownFnQueue[prio] = append(shutdownFnQueue[prio], f)
	sqM.Unlock()
	return f.client
}

// onShutdown will request a shutdown notifier.
func onShutdown(prio int) Notifier {
	sqM.Lock()
	n := make(Notifier, 1)
	shutdownQueue[prio] = append(shutdownQueue[prio], n)
	sqM.Unlock()
	return n
}

// OnSignal will start the shutdown when any of the given signals arrive
//
// A good shutdown default is
//    shutdown.OnSignal(0, os.Interrupt, syscall.SIGTERM)
// which will do shutdown on Ctrl+C and when the program is terminated.
func OnSignal(exitCode int, sig ...os.Signal) {
	// capture signal and shut down.
	c := make(chan os.Signal, 1)
	signal.Notify(c, sig...)
	go func() {
		for _ = range c {
			Shutdown()
			os.Exit(exitCode)
		}
	}()
}

// Exit performs shutdown operations and exits with the given exit code.
func Exit(code int) {
	Shutdown()
	os.Exit(code)
}

// Shutdown will signal all notifiers in three stages.
// It will first check that all locks have been released - see Lock()
func Shutdown() {
	srM.Lock()
	if shutdownRequested {
		srM.Unlock()
		// Wait till shutdown finished
		<-shutdownFinished
		return
	}
	shutdownRequested = true
	srM.Unlock()

	// Add a pre-shutdown function that waits for all locks to be released.
	PreShutdownFn(func() {
		srM.Lock()
		wait := wg
		srM.Unlock()
		wait.Wait()
	})

	sqM.Lock()
	for stage := 0; stage < 4; stage++ {
		srM.Lock()
		to := timeouts[stage]
		srM.Unlock()

		queue := shutdownQueue[stage]
		if len(queue) == 0 {
			continue
		}
		if stage == 0 {
			Logger.Println("Initiating shutdown")
		} else {
			Logger.Println("Shutdown stage", stage)
		}
		wait := make([]chan struct{}, len(queue))

		// Send notification to all waiting
		for i := range queue {
			wait[i] = make(chan struct{})
			queue[i] <- wait[i]
		}

		// Send notification to all function notifiers, but don't wait
		for _, notifier := range shutdownFnQueue[stage] {
			notifier.client <- make(chan struct{})
			close(notifier.client)
		}

		// We don't lock while we are waiting for notifiers to return
		sqM.Unlock()

		// Wait for all to return, no more than the shutdown delay
		timeout := time.After(to)

	brwait:
		for i := range wait {
			select {
			case <-wait[i]:
			case <-timeout:
				Logger.Println("timeout waiting to shutdown, forcing shutdown")
				break brwait
			}
		}
		sqM.Lock()
	}
	// Reset - mainly for tests.
	shutdownQueue = [4][]Notifier{}
	shutdownFnQueue = [4][]fnNotify{}
	close(shutdownFinished)
	sqM.Unlock()
}

// Started returns true if shutdown has been started.
// Note that shutdown can have been started before you check the value.
func Started() bool {
	srM.RLock()
	started := shutdownRequested
	srM.RUnlock()
	return started
}

var wg *sync.WaitGroup

func init() {
	wg = &sync.WaitGroup{}
}

// Wait will wait until shutdown has finished.
// This can be used to keep a main function from exiting
// until shutdown has been called, either by a goroutine
// or a signal.
func Wait() {
	<-shutdownFinished
}

// LogLockTimeouts can be disabled to disable log timeout warnings.
var LogLockTimeouts = true


// Lock will signal that you have a function running,
// that you do not want to be interrupted by a shutdown.
//
// The lock is created with a timeout equal to the length of the
// preshutdown stage at the time of creation. When that amount of
// time has expired the lock will be removed, and a warning will
// be printed.
//
// If the function returns nil shutdown has already been initiated,
// and you did not get a lock. You should therefore not call the returned
// function.
//
// If the function did not return nil, you should call the function to unlock
// the lock.
//
// You should not hold a lock when you start a shutdown.
func Lock() func() {
	srM.RLock()
	s := shutdownRequested
	if s {
		srM.RUnlock()
		return nil
	}
	wg.Add(1)
	srM.RUnlock()
	var release = make(chan struct{}, 0)
	var timeout = time.After(timeouts[0])

	// Store what called this
	var calledFrom string
	if LogLockTimeouts {
		_, file, line, _ := runtime.Caller(1)
		calledFrom = fmt.Sprintf("%s:%d", file, line)
	}
	go func() {
		select {
		case <-timeout:
		if LogLockTimeouts {
			Logger.Printf("warning: lock expired. Called from %s\n", calledFrom)
		}
		case <-release:
		}
		wg.Done()
	}()
	return func() { close(release) }
}

