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
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Stage contains stage information.
// Valid values for this is exported as variables StageN.
type Stage struct {
	n int
}

// LogPrinter is an interface for writing logging information.
// The writer must handle concurrent writes.
type LogPrinter interface {
	Printf(format string, v ...interface{})
}

var (
	// Logger used for output.
	// This can be exchanged with your own.
	Logger = LogPrinter(log.New(os.Stderr, "[shutdown]: ", log.LstdFlags))

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

// Internal notifier
type iNotifier struct {
	n          Notifier
	calledFrom string
}
type fnNotify struct {
	client   Notifier
	internal iNotifier
	cancel   chan struct{}
}

type logWrapper struct {
	w func(format string, v ...interface{})
}

func (l logWrapper) Printf(format string, v ...interface{}) {
	l.w(format, v...)
}

// SetLogPrinter will use the specified function to write logging information.
// The writer must handle concurrent writes.
func SetLogPrinter(fn func(format string, v ...interface{})) {
	Logger = logWrapper{w: fn}
}

//TODO(klauspost): These should be added to a struct for easier testing.
var sqM sync.Mutex // Mutex for below
var shutdownQueue [4][]iNotifier
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
// If the shutdown has already started this will not have any effect,
// but a goroutine will wait for the notifier to be triggered.
func (s *Notifier) Cancel() {
	srM.RLock()
	if shutdownRequested {
		srM.RUnlock()
		// Wait until we get the notification and close it:
		go func() {
			select {
			case v := <-*s:
				close(v)
			}
		}()
		return
	}
	srM.RUnlock()
	sqM.Lock()
	var a chan chan struct{}
	var b chan chan struct{}
	a = *s
	for n, sdq := range shutdownQueue {
		for i, qi := range sdq {
			b = qi.n
			if a == b {
				shutdownQueue[n] = append(shutdownQueue[n][:i], shutdownQueue[n][i+1:]...)
			}
		}
		for i, fn := range shutdownFnQueue[n] {
			b = fn.client
			if a == b {
				// Find the matching internal and remove that.
				for i := range shutdownQueue[n] {
					b = shutdownQueue[n][i].n
					if fn.internal.n == b {
						shutdownQueue[n] = append(shutdownQueue[n][:i], shutdownQueue[n][i+1:]...)
						break
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

// CancelWait will cancel a Notifier, or wait for it to become active if shutdown has been started.
// This will remove a notifier from the shutdown queue, and it will not be signalled when shutdown starts.
// If the shutdown has already started, this will wait for the notifier to be called and close it.
func (s *Notifier) CancelWait() {
	sqM.Lock()
	var a chan chan struct{}
	var b chan chan struct{}
	a = *s
	for n, sdq := range shutdownQueue {
		for i, qi := range sdq {
			b = qi.n
			if a == b {
				shutdownQueue[n] = append(shutdownQueue[n][:i], shutdownQueue[n][i+1:]...)
			}
		}
		for i, fn := range shutdownFnQueue[n] {
			b = fn.client
			if a == b {
				// Find the matching internal and remove that.
				for i := range shutdownQueue[n] {
					b = shutdownQueue[n][i].n
					if fn.internal.n == b {
						shutdownQueue[n] = append(shutdownQueue[n][:i], shutdownQueue[n][i+1:]...)
						break
					}
				}
				// Cancel, so the goroutine exits.
				close(fn.cancel)
				// Remove this
				shutdownFnQueue[n] = append(shutdownFnQueue[n][:i], shutdownFnQueue[n][i+1:]...)
			}
		}
	}
	srM.RLock()
	if shutdownRequested {
		sqM.Unlock()
		srM.RUnlock()
		// Wait until we get the notification and close it:
		select {
		case v := <-*s:
			close(v)
		}
		return
	}
	srM.RUnlock()
	sqM.Unlock()
}

// PreShutdown will return a Notifier that will be fired as soon as the shutdown
// is signalled, before locks are released.
// This allows to for instance send signals to upstream servers not to send more requests.
func PreShutdown() Notifier {
	return onShutdown(0, 1).n
}

// PreShutdownFn registers a function that will be called as soon as the shutdown
// is signalled, before locks are released.
// This allows to for instance send signals to upstream servers not to send more requests.
func PreShutdownFn(fn func()) Notifier {
	return onFunc(0, 1, fn)
}

// First returns a notifier that will be called in the first stage of shutdowns
func First() Notifier {
	return onShutdown(1, 1).n
}

// FirstFn executes a function in the first stage of the shutdown
func FirstFn(fn func()) Notifier {
	return onFunc(1, 1, fn)
}

// Second returns a notifier that will be called in the second stage of shutdowns
func Second() Notifier {
	return onShutdown(2, 1).n
}

// SecondFn executes a function in the second stage of the shutdown
func SecondFn(fn func()) Notifier {
	return onFunc(2, 1, fn)
}

// Third returns a notifier that will be called in the third stage of shutdowns
func Third() Notifier {
	return onShutdown(3, 1).n
}

// ThirdFn executes a function in the third stage of the shutdown
// The returned Notifier is only really useful for cancelling the shutdown function
func ThirdFn(fn func()) Notifier {
	return onFunc(3, 1, fn)
}

// Create a function notifier.
// depth is the call depth of the caller.
func onFunc(prio, depth int, fn func()) Notifier {
	f := fnNotify{
		internal: onShutdown(prio, depth+1),
		cancel:   make(chan struct{}),
		client:   make(Notifier, 1),
	}
	go func() {
		select {
		case <-f.cancel:
			return
		case c := <-f.internal.n:
			{
				defer func() {
					if r := recover(); r != nil {
						Logger.Printf("Error: Panic in shutdown function: %v (%v)", r, f.internal.calledFrom)
						Logger.Printf("%s", string(debug.Stack()))
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
// depth is the call depth of the caller.
func onShutdown(prio, depth int) iNotifier {
	sqM.Lock()
	n := make(Notifier, 1)
	in := iNotifier{n: n}
	if LogLockTimeouts {
		_, file, line, _ := runtime.Caller(depth + 1)
		in.calledFrom = fmt.Sprintf("%s:%d", file, line)
	}
	shutdownQueue[prio] = append(shutdownQueue[prio], in)
	sqM.Unlock()
	return in
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
		for range c {
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
	lwg := wg
	srM.Unlock()

	// Add a pre-shutdown function that waits for all locks to be released.
	PreShutdownFn(func() {
		lwg.Wait()
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
			Logger.Printf("Initiating shutdown %v", time.Now())
		} else {
			Logger.Printf("Shutdown stage %v", stage)
		}
		wait := make([]chan struct{}, len(queue))
		var calledFrom []string
		if LogLockTimeouts {
			calledFrom = make([]string, len(queue))
		}
		// Send notification to all waiting
		for i, n := range queue {
			wait[i] = make(chan struct{})
			if LogLockTimeouts {
				calledFrom[i] = n.calledFrom
			}
			queue[i].n <- wait[i]
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
			var tick <-chan time.Time
			if LogLockTimeouts {
				tick = time.Tick(StatusTimer)
			}
		wloop:
			for {
				select {
				case <-wait[i]:
					break wloop
				case <-timeout:
					if len(calledFrom) > 0 {
						Logger.Printf("Timed out notifier created at %s", calledFrom[i])
					}
					Logger.Printf("Timeout waiting to shutdown, forcing shutdown stage %v.", stage)
					break brwait
				case <-tick:
					if len(calledFrom) > 0 {
						Logger.Printf("Stage %d, waiting for notifier (%s)", stage, calledFrom[i])
					}
				}
			}
		}
		sqM.Lock()
	}
	// Reset - mainly for tests.
	shutdownQueue = [4][]iNotifier{}
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

var wg = &sync.WaitGroup{}

// Wait will wait until shutdown has finished.
// This can be used to keep a main function from exiting
// until shutdown has been called, either by a goroutine
// or a signal.
func Wait() {
	<-shutdownFinished
}

// LogLockTimeouts enables log timeout warnings
// and notifier status updates.
// Should not be changed once shutdown has started.
var LogLockTimeouts = true

// StatusTimer is the time between logging which notifiers are waiting to finish.
// Should not be changed once shutdown has started.
var StatusTimer = time.Minute

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
	if shutdownRequested {
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
	go func(wg *sync.WaitGroup) {
		select {
		case <-timeout:
			if LogLockTimeouts {
				Logger.Printf("warning: lock expired. Called from %s\n", calledFrom)
			}
		case <-release:
		}
		wg.Done()
	}(wg)
	return func() { close(release) }
}
