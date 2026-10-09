package volcano

import (
	"context"
	"testing"

	"github.com/gotomicro/ego/core/elog"
)

// TestGetOSSMetricsProbeUnsupported 探测不支持路径(AC-2):T1 定案「二期补」
// (probe-report §1.5/§3:指标注册表为空、云产品监控指标订阅未开通,bucket 数
// 占比 15.58% >15% 但探测不可用,按 spec 显式降级)——必须返回空切片 + nil
// error,不报 ERROR、不假装有数据。INFO 级日志由适配器发出(尽力而为语义,
// 失败计数不受影响)。
func TestGetOSSMetricsProbeUnsupported(t *testing.T) {
	adapter := NewTOSAdapter("ak", "sk", "cn-beijing", elog.DefaultLogger)

	metrics, err := adapter.GetOSSMetrics(context.Background(), "ark-auto-2100700292-cn-beijing-default", "2026-09-18", "2026-09-19")
	if err != nil {
		t.Fatalf("探测不支持应返回 nil error(尽力而为,不阻塞全流程), got %v", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("探测不支持 = %+v, want 空切片", metrics)
	}
}

// TestGetOSSMetricsProbeUnsupportedAnyArgs 桩不校验入参:探测不支持先于参数
// 校验,任何调用(含空参)都返回空切片 + nil error。
func TestGetOSSMetricsProbeUnsupportedAnyArgs(t *testing.T) {
	adapter := NewTOSAdapter("ak", "sk", "", elog.DefaultLogger)
	metrics, err := adapter.GetOSSMetrics(context.Background(), "", "", "")
	if err != nil || metrics == nil || len(metrics) != 0 {
		t.Fatalf("= (%v, %+v), want (nil, 空切片)", err, metrics)
	}
}
