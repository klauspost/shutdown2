// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

// +build !go1.7

package shutdown

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/net/context"
)

// otherContext is a Context that's not one of the types defined in context.go.
// This lets us test code paths that differ based on the underlying type of the
// Context.
type otherContext struct {
	context.Context
}

func TestCancelCtx(t *testing.T) {
	reset()
	SetTimeout(time.Second)
	defer close(startTimer(t))

	c1, _ := CancelCtx(context.Background())

	if got, want := fmt.Sprint(c1), "context.Background.WithCancel"; got != want {
		t.Errorf("c1.String() = %q want %q", got, want)
	}

	o := otherContext{c1}
	c2, _ := context.WithCancel(o)
	contexts := []context.Context{c1, o, c2}

	for i, c := range contexts {
		if d := c.Done(); d == nil {
			t.Errorf("c[%d].Done() == %v want non-nil", i, d)
		}
		if e := c.Err(); e != nil {
			t.Errorf("c[%d].Err() == %v want nil", i, e)
		}

		select {
		case x := <-c.Done():
			t.Errorf("<-c.Done() == %v want nothing (it should block)", x)
		default:
		}
	}

	Shutdown()
	time.Sleep(100 * time.Millisecond) // let cancellation propagate

	for i, c := range contexts {
		select {
		case <-c.Done():
		default:
			t.Errorf("<-c[%d].Done() blocked, but shouldn't have", i)
		}
		if e := c.Err(); e != context.Canceled {
			t.Errorf("c[%d].Err() == %v want %v", i, e, context.Canceled)
		}
	}
}

func TestCancelCtxN(t *testing.T) {
	reset()
	SetTimeout(time.Second)
	defer close(startTimer(t))
	stages := []Stage{StagePS, Stage1, Stage2, Stage3}
	contexts := []context.Context{}

	for _, stage := range stages {
		c1, _ := CancelCtxN(stage, context.Background())
		if got, want := fmt.Sprint(c1), "context.Background.WithCancel"; got != want {
			t.Errorf("c1.String() = %q want %q", got, want)
		}
		o := otherContext{c1}
		c2, _ := context.WithCancel(o)
		contexts = append(contexts, c1, o, c2)
	}

	for i, c := range contexts {
		if d := c.Done(); d == nil {
			t.Errorf("c[%d].Done() == %v want non-nil", i, d)
		}
		if e := c.Err(); e != nil {
			t.Errorf("c[%d].Err() == %v want nil", i, e)
		}

		select {
		case x := <-c.Done():
			t.Errorf("<-c.Done() == %v want nothing (it should block)", x)
		default:
		}
	}

	Shutdown()
	time.Sleep(100 * time.Millisecond) // let cancellation propagate

	for i, c := range contexts {
		select {
		case <-c.Done():
		default:
			t.Errorf("<-c[%d].Done() blocked, but shouldn't have", i)
		}
		if e := c.Err(); e != context.Canceled {
			t.Errorf("c[%d].Err() == %v want %v", i, e, context.Canceled)
		}
	}
}

func TestCancelCtxNShutdown(t *testing.T) {
	reset()
	SetTimeout(time.Second)
	defer close(startTimer(t))
	stages := []Stage{StagePS, Stage1, Stage2, Stage3}
	contexts := []context.Context{}

	for _, stage := range stages {
		c1, cancel1 := CancelCtxN(stage, context.Background())
		o := otherContext{c1}
		c2, _ := context.WithCancel(o)
		contexts = append(contexts, c1, o, c2)
		cancel1()
	}

	time.Sleep(100 * time.Millisecond) // let cancellation propagate

	for i, c := range contexts {
		select {
		case <-c.Done():
		default:
			t.Errorf("<-c[%d].Done() blocked, but shouldn't have", i)
		}
		if e := c.Err(); e != context.Canceled {
			t.Errorf("c[%d].Err() == %v want %v", i, e, context.Canceled)
		}
	}

	// Ensure shutdown is not blocking
	Shutdown()
}
