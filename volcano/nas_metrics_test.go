package volcano

import (
	"context"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
)

// TestGetNASMetricsProbeUnsupported 探测不支持路径(AC-2/AC-5):T1 定案
// 「二期补」(probe-report §1.4/§3,指标注册表为空、订阅未开通,双口径占比
// ≤15% 不触发升格)——必须返回空切片 + nil error,不报 ERROR、不假装有数据。
// INFO 级日志由适配器发出(尽力而为语义,失败计数不受影响)。
func TestGetNASMetricsProbeUnsupported(t *testing.T) {
	adapter := NewNASAdapter("ak", "sk", "cn-beijing", elog.DefaultLogger)

	metrics, err := adapter.GetNASMetrics(context.Background(), "fs-1", "nas-1", "cn-beijing", "2026-09-18", "2026-09-19")
	if err != nil {
		t.Fatalf("探测不支持应返回 nil error(尽力而为,不阻塞全流程), got %v", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("探测不支持 = %+v, want 空切片", metrics)
	}
	var _ types.NASMetric
	_ = adapter
}

// TestGetNASMetricsProbeUnsupportedEmptyFSID 无参调用同样空 + nil(桩不校验
// 入参:探测不支持先于参数校验,任何调用都返回空集)。
func TestGetNASMetricsProbeUnsupportedEmptyFSID(t *testing.T) {
	adapter := NewNASAdapter("ak", "sk", "", elog.DefaultLogger)
	metrics, err := adapter.GetNASMetrics(context.Background(), "", "", "", "", "")
	if err != nil || len(metrics) != 0 {
		t.Fatalf("= (%v, %d), want (nil, 0)", err, len(metrics))
	}
}
