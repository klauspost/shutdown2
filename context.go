// Copyright (c) 2015 Klaus Post, released under MIT License. See LICENSE file.

package shutdown

import "context"

// CancelCtx will cancel the supplied context when shutdown starts.
// The returned context must be cancelled when done similar to
// https://golang.org/pkg/context/#WithCancel
func CancelCtx(parent context.Context) (ctx context.Context, cancel context.CancelFunc) {
	return cancelContext(parent, StagePS)
}

// CancelCtxN will cancel the supplied context at a supplied shutdown stage.
// The returned context must be cancelled when done similar to
// https://golang.org/pkg/context/#WithCancel
func CancelCtxN(parent context.Context, s Stage) (ctx context.Context, cancel context.CancelFunc) {
	return cancelContext(parent, s)
}

func cancelContext(parent context.Context, s Stage) (ctx context.Context, cancel context.CancelFunc) {
	ctx, cancel = context.WithCancel(parent)
	f := onShutdown(s.n, 2, []interface{}{parent}).n
	if f == nil {
		cancel()
		return ctx, cancel
	}
	go func() {
		select {
		case <-ctx.Done():
			f.CancelWait()
		case v := <-f:
			cancel()
			close(v)
		}
	}()
	return ctx, cancel
}
