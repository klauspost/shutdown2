// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

package shutdown

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func reset() {
	SetTimeout(1 * time.Second)
	sqM.Lock()
	defer sqM.Unlock()
	srM.Lock()
	defer srM.Unlock()
	wg = &sync.WaitGroup{}
	shutdownRequested = false
	shutdownQueue = [4][]Notifier{}
	shutdownFnQueue = [4][]fnNotify{}
	shutdownFinished = make(chan struct{})
}

func startTimer(t *testing.T) chan struct{} {
	finished := make(chan struct{}, 0)
	srM.RLock()
	var to time.Duration
	for i := range timeouts {
		to += timeouts[i]
	}
	srM.RUnlock()
	// Add some extra time.
	toc := time.After((to * 10) / 9)
	go func() {
		select {
		case <-toc:
			panic("unexpected timeout while running test")
		case <-finished:
			return

		}
	}()
	return finished
}

func TestBasic(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := First()
	ok := false
	go func() {
		select {
		case n := <-f:
			ok = true
			close(n)
		}
	}()
	Shutdown()
	if !ok {
		t.Fatal("did not get expected shutdown signal")
	}
	if !Started() {
		t.Fatal("shutdown not marked started")
	}
	// Should just return at once.
	Shutdown()
	// Should also return at once.
	Wait()
}

func TestPreShutdown(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := PreShutdown()
	ok := false
	l := Lock()
	go func() {
		select {
		case n := <-f:
			ok = true
			l()
			close(n)
		}
	}()
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Second {
		t.Fatalf("timeout time was hit unexpected:%v", time.Now().Sub(tn))
	}

	if !ok {
		t.Fatal("did not get expected shutdown signal")
	}
	if !Started() {
		t.Fatal("shutdown not marked started")
	}
}

func TestCancel(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := First()
	ok := false
	go func() {
		select {
		case n := <-f:
			ok = true
			close(n)
		}
	}()
	f.Cancel()
	Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestCancel2(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f2 := First()
	f := First()
	var ok, ok2 bool

	go func() {
		select {
		case n := <-f:
			ok = true
			close(n)
		}
	}()
	go func() {
		select {
		case n := <-f2:
			ok2 = true
			close(n)
		}
	}()
	f.Cancel()
	Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
	if !ok2 {
		t.Fatal("missing shutdown signal")
	}
}

func TestCancelWait(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := First()
	ok := false
	go func() {
		select {
		case n := <-f:
			ok = true
			close(n)
		}
	}()
	f.CancelWait()
	Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestCancelWait2(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f2 := First()
	f := First()
	var ok, ok2 bool

	go func() {
		select {
		case n := <-f:
			ok = true
			close(n)
		}
	}()
	go func() {
		select {
		case n := <-f2:
			ok2 = true
			close(n)
		}
	}()
	f.CancelWait()
	Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
	if !ok2 {
		t.Fatal("missing shutdown signal")
	}
}

// TestCancelWait3 assert that we can CancelWait, and that wait will wait until the
// specified stage.
func TestCancelWait3(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := First()
	var ok, ok2, ok3 bool
	f2 := Second()
	cancelled := make(chan struct{}, 0)
	reached := make(chan struct{}, 0)
	p2started := make(chan struct{}, 0)
	_ = SecondFn(func(){
		<-p2started
		close(reached)
	})
	var wg sync.WaitGroup
	go func() {
		select {
		case v := <-f2:
			ok3 = true
			close(v)
		case <-cancelled:
		}
	}()
	wg.Add(1)
	go func() {
		select {
		case n := <-f:
			ok = true
			go func() {
				wg.Done()
				close(cancelled)
				f2.CancelWait()
				// We should be at stage 2
				close(p2started)
				<-reached
			}()
			wg.Wait()
			time.Sleep(10 * time.Millisecond)
			close(n)
		}

	}()
	Shutdown()
	if !ok {
		t.Fatal("missing shutdown signal")
	}
	if ok2 {
		t.Fatal("got unexpected shutdown signal")
	}
	if ok3 {
		t.Fatal("got unexpected shutdown signal")
	}
}

// TestCancelWait4 assert that we can CancelWait on a previous stage,
// and it doesn't block.
func TestCancelWait4(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := Second()
	var ok bool
	f2 := First()
	go func() {
		select {
		case n := <-f:
			// Should not wait
			f2.CancelWait()
			ok = true
			close(n)
		}

	}()
	Shutdown()
	if !ok {
		t.Fatal("missing shutdown signal")
	}
}

func TestFnCancelWait(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := First()
	var ok, ok2 bool
	f2 := SecondFn(func() {
		ok2 = true
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		select {
		case n := <-f:
			ok = true
			go func() {
				wg.Done()
				f2.CancelWait()
			}()
			wg.Wait()
			time.Sleep(10 * time.Millisecond)
			close(n)
		}

	}()
	Shutdown()
	if !ok {
		t.Fatal("missing shutdown signal")
	}
	if ok2 {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestWait(t *testing.T) {
	reset()
	defer close(startTimer(t))
	ok := make(chan bool)
	go func() {
		Wait()
		close(ok)
	}()
	// Wait a little - enough to fail very often.
	time.Sleep(time.Millisecond * 10)

	select {
	case <-ok:
		t.Fatal("Wait returned before shutdown finished")
	default:
	}

	Shutdown()

	// ok should return, otherwise we wait for timeout, which will fail the test
	<-ok
}

func TestTimeout(t *testing.T) {
	reset()
	SetTimeout(time.Millisecond * 100)
	defer close(startTimer(t))
	f := First()
	go func() {
		select {
		case <-f:
		}
	}()
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
	if !Started() {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestTimeoutN(t *testing.T) {
	reset()
	SetTimeout(time.Second * 2)
	SetTimeoutN(Stage1, time.Millisecond*100)
	defer close(startTimer(t))
	f := First()
	go func() {
		select {
		case <-f:
		}
	}()
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
	if !Started() {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestTimeoutN2(t *testing.T) {
	reset()
	SetTimeout(time.Millisecond * 100)
	SetTimeoutN(Stage2, time.Second*2)
	defer close(startTimer(t))
	f := First()
	go func() {
		select {
		case <-f:
		}
	}()
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
	if !Started() {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestLock(t *testing.T) {
	reset()
	defer close(startTimer(t))
	f := First()
	ok := false
	go func() {
		select {
		case n := <-f:
			ok = true
			close(n)
		}
	}()
	got := Lock()
	if got == nil {
		t.Fatal("Unable to aquire lock")
	}
	got()

	// Start 10 goroutines that aquire a lock.
	var wg1, wg2 sync.WaitGroup
	wg1.Add(10)
	wg2.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg1.Done()
			wg2.Done() // Signal we are ready to take the lock
			l := Lock()
			if l != nil {
				time.Sleep(timeouts[0] / 2)
				l()
			}
		}()
	}
	// Wait for all goroutines to have aquired the lock
	wg2.Wait()
	Shutdown()
	if !ok {
		t.Fatal("shutdown signal not received")
	}
	if !Started() {
		t.Fatal("expected that shutdown had started")
	}
	wg1.Wait()
}

func TestLockUnrelease(t *testing.T) {
	reset()
	defer close(startTimer(t))
	SetTimeout(time.Millisecond * 100)
	got := Lock()
	if got == nil {
		t.Fatal("Unable to aquire lock")
	}
	tn := time.Now()
	Shutdown()
	dur := time.Now().Sub(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Now().Sub(tn))
	}
	if !Started() {
		t.Fatal("expected that shutdown had started")
	}
}

func TestOrder(t *testing.T) {
	reset()
	defer close(startTimer(t))

	t3 := Third()
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t2 := Second()
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t1 := First()
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t0 := PreShutdown()
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	var ok0, ok1, ok2, ok3 bool
	go func() {
		for {
			select {
			//t0 must be first
			case n := <-t0:
				if ok0 || ok1 || ok2 || ok3 {
					t.Fatal("unexpected order", ok0, ok1, ok2, ok3)
				}
				ok0 = true
				close(n)
			case n := <-t1:
				if !ok0 || ok1 || ok2 || ok3 {
					t.Fatal("unexpected order", ok0, ok1, ok2, ok3)
				}
				ok1 = true
				close(n)
			case n := <-t2:
				if !ok0 || !ok1 || ok2 || ok3 {
					t.Fatal("unexpected order", ok0, ok1, ok2, ok3)
				}
				ok2 = true
				close(n)
			case n := <-t3:
				if !ok0 || !ok1 || !ok2 || ok3 {
					t.Fatal("unexpected order", ok0, ok1, ok2, ok3)
				}
				ok3 = true
				close(n)
				return
			}
		}
	}()
	if ok0 || ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok0, ok1, ok2, ok3)
	}

	Shutdown()
	if !ok0 || !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok0, ok1, ok2, ok3)
	}
}

func TestRecursive(t *testing.T) {
	reset()
	defer close(startTimer(t))

	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t1 := First()
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	var ok1, ok2, ok3 bool
	go func() {
		for {
			select {
			case n := <-t1:
				ok1 = true
				t2 := Second()
				close(n)
				select {
				case n := <-t2:
					ok2 = true
					t3 := Third()
					close(n)
					select {
					case n := <-t3:
						ok3 = true
						close(n)
						return
					}
				}
			}
		}
	}()
	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	Shutdown()
	if !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

func TestBasicFn(t *testing.T) {
	reset()
	defer close(startTimer(t))
	gotcall := false

	// Register a function
	_ = FirstFn(func() {
		gotcall = true
	})

	// Start shutdown
	Shutdown()
	if !gotcall {
		t.Fatal("did not get expected shutdown signal")
	}
}

func setBool(i *bool) func() {
	return func() {
		*i = true
	}
}

func TestFnOrder(t *testing.T) {
	reset()
	defer close(startTimer(t))

	var ok1, ok2, ok3 bool
	_ = ThirdFn(setBool(&ok3))
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = SecondFn(setBool(&ok2))
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = FirstFn(setBool(&ok1))
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	Shutdown()

	if !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

func TestFnRecursive(t *testing.T) {
	reset()
	defer close(startTimer(t))

	var ok1, ok2, ok3 bool

	_ = FirstFn(func() {
		ok1 = true
		_ = SecondFn(func() {
			ok2 = true
			_ = ThirdFn(func() {
				ok3 = true
			})
		})
	})

	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	Shutdown()

	if !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

// When setting First or Second inside stage three they should be ignored.
func TestFnRecursiveRev(t *testing.T) {
	reset()
	defer close(startTimer(t))

	var ok1, ok2, ok3 bool

	_ = ThirdFn(func() {
		ok3 = true
		_ = SecondFn(func() {
			ok2 = true
		})
		_ = FirstFn(func() {
			ok1 = true
		})
	})

	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	Shutdown()

	if ok1 || ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

func TestFnCancel(t *testing.T) {
	reset()
	defer close(startTimer(t))
	var g0, g1, g2, g3 bool

	// Register a function
	notp := PreShutdownFn(func() {
		g0 = true
	})
	not1 := FirstFn(func() {
		g1 = true
	})
	not2 := SecondFn(func() {
		g2 = true
	})
	not3 := ThirdFn(func() {
		g3 = true
	})

	notp.Cancel()
	not1.Cancel()
	not2.Cancel()
	not3.Cancel()

	// Start shutdown
	Shutdown()
	if g1 || g2 || g3 || g0 {
		t.Fatal("got unexpected shutdown signal", g0, g1, g2, g3)
	}
}

func TestFnPanic(t *testing.T) {
	reset()
	defer close(startTimer(t))
	gotcall := false

	// Register a function
	_ = FirstFn(func() {
		gotcall = true
		panic("This is expected")
	})

	// Start shutdown
	Shutdown()
	if !gotcall {
		t.Fatal("did not get expected shutdown signal")
	}
}

func TestFnNotify(t *testing.T) {
	reset()
	defer close(startTimer(t))
	gotcall := false

	// Register a function
	fn := FirstFn(func() {
		gotcall = true
	})

	// Start shutdown
	Shutdown()

	// This must have a notification
	_, ok := <-fn
	if !ok {
		t.Fatal("Notifier was closed before a notification")
	}
	// After this the channel must be closed
	_, ok = <-fn
	if ok {
		t.Fatal("Notifier was not closed after initial notification")
	}
	if !gotcall {
		t.Fatal("did not get expected shutdown signal")
	}
}

func TestFnSingleCancel(t *testing.T) {
	reset()
	defer close(startTimer(t))

	var ok1, ok2, ok3, okcancel bool
	_ = ThirdFn(func() {
		ok3 = true
	})
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = SecondFn(func() {
		ok2 = true
	})
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	cancel := SecondFn(func() {
		okcancel = true
	})
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = FirstFn(func() {
		ok1 = true
	})
	if Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 || okcancel {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3, okcancel)
	}

	cancel.Cancel()

	Shutdown()

	if !ok1 || !ok2 || !ok3 || okcancel {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3, okcancel)
	}
}

// Get a notifier and perform our own code when we shutdown
func ExampleNotifier() {
	shutdown := First()
	select {
	case n := <-shutdown:
		// Do shutdown code ...

		// Signal we are done
		close(n)
	}
}

// Get a notifier and perform our own function when we shutdown
func Example_functions() {
	_ = FirstFn(func() {
		// This function is called on shutdown
		fmt.Println("First shutdown stage called")
	})

	// Will print the parameter when Shutdown() is called
}

// Note that the same effect of this example can also be achieved using the
// WrapHandlerFunc helper.
func ExampleLock() {
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		// Get a lock while we have the lock, the server will not shut down.
		lock := Lock()
		if lock != nil {
			defer lock()
		} else {
			// We are currently shutting down, return http.StatusServiceUnavailable
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// ...
	})
	http.ListenAndServe(":8080", nil)
}

// Change timeout for a single stage
func ExampleSetTimeoutN() {
	// Set timout for all stages
	SetTimeout(time.Second)

	// But give second stage more time
	SetTimeoutN(Stage2, time.Second*10)
}

// This is an example, that could be your main function.
//
// We wait for jobs to finish in another goroutine, from
// where we initialize the shutdown.
//
// This is of course not a real-world problem, but there are many
// cases where you would want to initialize shutdown from other places than
// your main function, and where you would still like it to be able to
// do some final cleanup.
func ExampleWait() {
	x := make([]struct{}, 10)
	var wg sync.WaitGroup

	wg.Add(len(x))
	for i := range x {
		go func(i int) {
			time.Sleep(time.Millisecond * time.Duration(i))
			wg.Done()
		}(i)
	}

	// ignore this reset, for test purposes only
	reset()

	// Wait for the jobs above to finish
	go func() {
		wg.Wait()
		fmt.Println("jobs done")
		Shutdown()
	}()

	// Since this is main, we wait for a shutdown to occur before
	// exiting.
	Wait()
	fmt.Println("exiting main")

	// Note than the output will always be in this order.

	// Output: jobs done
	// exiting main
}
