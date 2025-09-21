package worker

import (
	"sync"
)

// Pool 是一个工作池结构
type Pool struct {
	work chan func()
	sem  chan struct{}
	wg   sync.WaitGroup
}

// NewPool 创建一个新的工作池
func NewPool(size int) *Pool {
	return &Pool{
		work: make(chan func()),
		sem:  make(chan struct{}, size),
	}
}

// Submit 提交一个任务到工作池
func (p *Pool) Submit(job func()) {
	p.sem <- struct{}{} // 获取信号量
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }() // 释放信号量
		job()
	}()
}

// Close 关闭工作池并等待所有任务完成
func (p *Pool) Close() {
	close(p.work)
	p.wg.Wait()
}