package tsync

import (
	"runtime/debug"
	"sync"
	"github.com/qist/tvgate/logger"
)

type WaitGroup struct {
	sync.WaitGroup
}

func (wg *WaitGroup) Go(f func()) {
	wg.Add(1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogPrintf("[WaitGroup.Go] goroutine panic: %v\n%s", r, debug.Stack())
			}
			wg.Done()
		}()

		f()
	}()
}