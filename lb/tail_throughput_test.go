package lb

import (
	"testing"
	"time"
)

// buildMarks 生成每秒 N 次的累计字节记录。
func buildMarks(bytesPerSec int64, seconds int, step time.Duration) (int64, []probeMark) {
	var total int64
	marks := make([]probeMark, 0, int(float64(seconds)/step.Seconds())+1)
	for t := time.Duration(0); t <= time.Duration(seconds)*time.Second; t += step {
		total += int64(float64(bytesPerSec) * step.Seconds())
		marks = append(marks, probeMark{at: t, n: total})
	}
	return total, marks
}

// TestTailThroughputIgnoresBurst 验证「前段突发、随后限速」不会被全程平均高估：
// 前 6 秒跑 4Mbps，之后连接停住直到 probe_timeout，后段窗口应为 0。
func TestTailThroughputIgnoresBurst(t *testing.T) {
	total, marks := buildMarks(500_000, 6, 500*time.Millisecond)
	// 之后 4 秒没有任何数据，10 秒时探测超时结束。
	got := tailThroughput(total, 10*time.Second, marks)
	if got != 0 {
		t.Fatalf("突然后停住应记 0 吞吐，got %d", got)
	}
}

// TestTailThroughputStableNode 验证稳定节点不会被后段窗口误伤。
func TestTailThroughputStableNode(t *testing.T) {
	total, marks := buildMarks(500_000, 10, time.Second) // 4Mbps
	got := tailThroughput(total, 10*time.Second, marks)
	want := int64(500_000)
	if got < want*8/10 || got > want*12/10 {
		t.Fatalf("稳定节点后段吞吐应接近 %d，got %d", want, got)
	}
}

// TestTailThroughputShortSampleUsesAverage 高速出口不足一个窗口就 EOF 时，
// 退回全程平均，避免把「很快读完」判成 0。
func TestTailThroughputShortSampleUsesAverage(t *testing.T) {
	total, marks := buildMarks(2_000_000, 1, 100*time.Millisecond) // 16Mbps
	got := tailThroughput(total, time.Second, marks)
	if got <= 0 {
		t.Fatalf("短样本应回退全程平均，got %d", got)
	}
}

// TestTailThroughputIgnoresTinySample 播放列表等小正文不算吞吐。
func TestTailThroughputIgnoresTinySample(t *testing.T) {
	total, marks := buildMarks(10_000, 1, 100*time.Millisecond) // 约 11KB
	if got := tailThroughput(total, time.Second, marks); got != -1 {
		t.Fatalf("小正文应返回无样本 -1，got %d", got)
	}
}
