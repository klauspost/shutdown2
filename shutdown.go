// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

// Package shutdown provides management of your shutdown process.
//
// The package will enable you to get notifications for your application and handle the shutdown process.
//
// # See more information about the how to use it in the README.md file
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

	// LoggerMu is a mutex for the Logger
	LoggerMu sync.Mutex

	// StagePS indicates the pre shutdown stage when waiting for locks to be released.
	StagePS = Stage{0}

	// Stage1 Indicates first stage of timeouts.
	Stage1 = Stage{1}

	// Stage2 Indicates second stage of timeouts.
	Stage2 = Stage{2}

	// Stage3 indicates third stage of timeouts.
	Stage3 = Stage{3}

	// WarningPrefix is printed before warnings.
	WarningPrefix = "WARN: "

	// ErrorPrefix is printed before errors.
	ErrorPrefix = "ERROR: "

	// LogLockTimeouts enables log timeout warnings
	// and notifier status updates.
	// Should not be changed once shutdown has started.
	LogLockTimeouts = true

	// StatusTimer is the time between logging which notifiers are waiting to finish.
	// Should not be changed once shutdown has started.
	StatusTimer = time.Minute

	sqM              sync.Mutex // Mutex for below
	shutdownQueue    [4][]iNotifier
	shutdownFnQueue  [4][]fnNotify
	shutdownFinished = make(chan struct{}, 0) // Closed when shutdown has finished
	currentStage     = Stage{-1}

	srM                 sync.RWMutex // Mutex for below
	shutdownRequested   = false
	shutdownRequestedCh = make(chan struct{})
	timeouts            = [4]time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second}

	onTimeOut func(s Stage, ctx string)
	wg        = &sync.WaitGroup{}
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
func SetLogPrinter(fn func(format string, v ...interface{})) {
	LoggerMu.Lock()
	Logger = logWrapper{w: fn}
	LoggerMu.Unlock()
}

// OnTimeout allows you to get a notification if a shutdown stage times out.
// The stage and the context of the hanging shutdown/lock function is returned.
func OnTimeout(fn func(Stage, string)) {
	srM.Lock()
	onTimeOut = fn
	srM.Unlock()
}

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
func (s Notifier) Cancel() {
	if s == nil {
		return
	}
	srM.RLock()
	if shutdownRequested {
		srM.RUnlock()
		// Wait until we get the notification and close it:
		go func() {
			select {
			case v := <-s:
				close(v)
			}
		}()
		return
	}
	srM.RUnlock()
	sqM.Lock()
	var a chan chan struct{}
	var b chan chan struct{}
	a = s
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
// If the notifier is nil (requested after its stage has started), it will return at once.
// If the shutdown has already started, this will wait for the notifier to be called and close it.
func (s Notifier) CancelWait() {
	if s == nil {
		return
	}
	sqM.Lock()
	var a chan chan struct{}
	var b chan chan struct{}
	a = s
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
		case v := <-s:
			close(v)
		}
		return
	}
	srM.RUnlock()
	sqM.Unlock()
}

// PreShutdown will return a Notifier that will be fired as soon as the shutdown.
// is signalled, before locks are released.
// This allows to for instance send signals to upstream servers not to send more requests.
// The context is printed if LogLockTimeouts is enabled.
func PreShutdown(ctx ...interface{}) Notifier {
	return onShutdown(0, 1, ctx).n
}

// PreShutdownFn registers a function that will be called as soon as the shutdown.
// is signalled, before locks are released.
// This allows to for instance send signals to upstream servers not to send more requests.
// The context is printed if LogLockTimeouts is enabled.
func PreShutdownFn(fn func(), ctx ...interface{}) Notifier {
	return onFunc(0, 1, fn, ctx)
}

// First returns a notifier that will be called in the first stage of shutdowns.
// If shutdown has started and this stage has already been reached, nil will be returned.
// The context is printed if LogLockTimeouts is enabled.
func First(ctx ...interface{}) Notifier {
	return onShutdown(1, 1, ctx).n
}

// FirstFn executes a function in the first stage of the shutdown
// If shutdown has started and this stage has already been reached, nil will be returned.
// The context is printed if LogLockTimeouts is enabled.
func FirstFn(fn func(), ctx ...interface{}) Notifier {
	return onFunc(1, 1, fn, ctx)
}

// Second returns a notifier that will be called in the second stage of shutdowns.
// If shutdown has started and this stage has already been reached, nil will be returned.
// The context is printed if LogLockTimeouts is enabled.
func Second(ctx ...interface{}) Notifier {
	return onShutdown(2, 1, ctx).n
}

// SecondFn executes a function in the second stage of the shutdown.
// If shutdown has started and this stage has already been reached, nil will be returned.
// The context is printed if LogLockTimeouts is enabled.
func SecondFn(fn func(), ctx ...interface{}) Notifier {
	return onFunc(2, 1, fn, ctx)
}

// Third returns a notifier that will be called in the third stage of shutdowns.
// If shutdown has started and this stage has already been reached, nil will be returned.
// The context is printed if LogLockTimeouts is enabled.
func Third(ctx ...interface{}) Notifier {
	return onShutdown(3, 1, ctx).n
}

// ThirdFn executes a function in the third stage of the shutdown.
// If shutdown has started and this stage has already been reached, nil will be returned.
// The context is printed if LogLockTimeouts is enabled.
func ThirdFn(fn func(), ctx ...interface{}) Notifier {
	return onFunc(3, 1, fn, ctx)
}

// Create a function notifier.
// depth is the call depth of the caller.
func onFunc(prio, depth int, fn func(), ctx []interface{}) Notifier {
	f := fnNotify{
		internal: onShutdown(prio, depth+1, ctx),
		cancel:   make(chan struct{}),
		client:   make(Notifier, 1),
	}
	if f.internal.n == nil {
		return nil
	}
	go func() {
		select {
		case <-f.cancel:
			return
		case c := <-f.internal.n:
			{
				defer func() {
					if r := recover(); r != nil {
						LoggerMu.Lock()
						Logger.Printf(ErrorPrefix+"Panic in shutdown function: %v (%v)", r, f.internal.calledFrom)
						Logger.Printf("%s", string(debug.Stack()))
						LoggerMu.Unlock()
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
func onShutdown(prio, depth int, ctx []interface{}) iNotifier {
	sqM.Lock()
	if currentStage.n >= prio {
		sqM.Unlock()
		return iNotifier{n: nil}
	}
	n := make(Notifier, 1)
	in := iNotifier{n: n}
	if LogLockTimeouts {
		_, file, line, _ := runtime.Caller(depth + 1)
		in.calledFrom = fmt.Sprintf("%s:%d", file, line)
		if len(ctx) != 0 {
			in.calledFrom = fmt.Sprintf("%v - %s", ctx, in.calledFrom)
		}
	}
	shutdownQueue[prio] = append(shutdownQueue[prio], in)
	sqM.Unlock()
	return in
}

// OnSignal will start the shutdown when any of the given signals arrive
//
// A good shutdown default is
//
//	shutdown.OnSignal(0, os.Interrupt, syscall.SIGTERM)
//
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
	close(shutdownRequestedCh)
	lwg := wg
	onTimeOutFn := onTimeOut
	srM.Unlock()

	// Add a pre-shutdown function that waits for all locks to be released.
	PreShutdownFn(func() {
		lwg.Wait()
	})

	sqM.Lock()
	for stage := 0; stage < 4; stage++ {
		srM.Lock()
		to := timeouts[stage]
		currentStage = Stage{stage}
		srM.Unlock()

		queue := shutdownQueue[stage]
		if len(queue) == 0 {
			continue
		}
		LoggerMu.Lock()
		if stage == 0 {
			Logger.Printf("Initiating shutdown %v", time.Now())
		} else {
			Logger.Printf("Shutdown stage %v", stage)
		}
		LoggerMu.Unlock()
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
					LoggerMu.Lock()
					if len(calledFrom) > 0 {
						srM.RLock()
						if onTimeOutFn != nil {
							onTimeOutFn(Stage{n: stage}, calledFrom[i])
						}
						srM.RUnlock()
						Logger.Printf(ErrorPrefix+"Notifier Timed Out: %s", calledFrom[i])
					}
					Logger.Printf(ErrorPrefix+"Timeout waiting to shutdown, forcing shutdown stage %v.", stage)
					LoggerMu.Unlock()
					break brwait
				case <-tick:
					if len(calledFrom) > 0 {
						LoggerMu.Lock()
						Logger.Printf(WarningPrefix+"Stage %d, waiting for notifier (%s)", stage, calledFrom[i])
						LoggerMu.Unlock()
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

// StartedCh returns a channel that is closed once shutdown has started.
func StartedCh() <-chan struct{} {
	return shutdownRequestedCh
}

// Wait will wait until shutdown has finished.
// This can be used to keep a main function from exiting
// until shutdown has been called, either by a goroutine
// or a signal.
func Wait() {
	<-shutdownFinished
}

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
//
// For easier debugging you can send a context that will be printed if the lock
// times out. All supplied context is printed with '%v' formatting.
func Lock(ctx ...interface{}) func() {
	srM.RLock()
	if shutdownRequested {
		srM.RUnlock()
		return nil
	}
	wg.Add(1)
	onTimeOutFn := onTimeOut
	srM.RUnlock()
	var release = make(chan struct{}, 0)
	var timeout = time.After(timeouts[0])

	// Store what called this
	var calledFrom string
	if LogLockTimeouts {
		_, file, line, _ := runtime.Caller(1)
		if len(ctx) > 0 {
			calledFrom = fmt.Sprintf("%v. ", ctx)
		}
		calledFrom = fmt.Sprintf("%sCalled from %s:%d", calledFrom, file, line)
	}

	go func(wg *sync.WaitGroup) {
		select {
		case <-timeout:
			if onTimeOutFn != nil {
				onTimeOutFn(StagePS, calledFrom)
			}
			if LogLockTimeouts {
				LoggerMu.Lock()
				Logger.Printf(WarningPrefix+"Lock expired! %s", calledFrom)
				LoggerMu.Unlock()
			}
		case <-release:
		}
		wg.Done()
	}(wg)
	return func() { close(release) }
}
