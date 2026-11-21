package lb

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRTSPSampleBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if got := rtspSampleBudget(ctx); got != rtspSampleWindow {
		t.Fatalf("充足余量时应采满 %v，实际 %v", rtspSampleWindow, got)
	}
}

func TestRTSPSampleBudgetSkipsNearDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if got := rtspSampleBudget(ctx); got != 0 {
		t.Fatalf("剩余时间不足时应放弃采样，实际 %v", got)
	}
}

func TestRTSPSampleThroughput(t *testing.T) {
	var recv atomic.Int64
	recv.Store(64 << 10)
	if tp := sampleRTPThroughput(context.Background(), &recv, 40*time.Millisecond); tp <= 0 {
		t.Fatalf("应计算出正吞吐，实际 %d", tp)
	}
}

func TestRTSPSampleThroughputTooSmall(t *testing.T) {
	var recv atomic.Int64
	recv.Store(1 << 10)
	if tp := sampleRTPThroughput(context.Background(), &recv, 20*time.Millisecond); tp != -1 {
		t.Fatalf("样本不足时应返回 -1，实际 %d", tp)
	}
}
