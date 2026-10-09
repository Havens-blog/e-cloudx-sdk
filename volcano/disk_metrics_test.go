package volcano

import (
	"context"
	"testing"

	"github.com/gotomicro/ego/core/elog"
)

// TestGetDiskMetricsProbeUnsupported 探测不支持路径(AC:尽力而为失败双路径之
// 「探测不支持」):T1 定案「二期补」(disk-ops-insight probe-report §1.5/§3/§4,
// 2026-09-21 运行时实测,只读凭证——真实磁盘 + 2240 组合候选矩阵全部 metric not
// found,阳性对照同样 not found,云产品监控指标订阅未开通;磁盘数占比 50.96%、
// 容量占比 46.33% 均 >15% 但探测不可用 → 按 spec 显式降级「二期补」并在发布
// 说明承诺补采窗口,不触发升格)——必须返回空切片 + nil error,不报 ERROR、
// 不假装有数据。INFO 级日志由适配器发出(尽力而为语义,失败计数不受影响)。
func TestGetDiskMetricsProbeUnsupported(t *testing.T) {
	adapter := NewDiskAdapter("ak", "sk", "cn-beijing", elog.DefaultLogger)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "vol-1", "data-disk-1", "cn-beijing", "2026-09-19", "2026-09-20")
	if err != nil {
		t.Fatalf("探测不支持应返回 nil error(尽力而为,不阻塞全流程), got %v", err)
	}
	if metrics == nil || len(metrics) != 0 {
		t.Fatalf("探测不支持 = %+v, want 空切片", metrics)
	}
}

// TestGetDiskMetricsProbeUnsupportedEmptyArgs 无参调用同样空 + nil(桩不校验
// 入参:探测不支持先于参数校验,任何调用都返回空集)。
func TestGetDiskMetricsProbeUnsupportedEmptyArgs(t *testing.T) {
	adapter := NewDiskAdapter("ak", "sk", "", elog.DefaultLogger)
	metrics, err := adapter.GetDiskMetrics(context.Background(), "", "", "", "", "")
	if err != nil || len(metrics) != 0 {
		t.Fatalf("= (%v, %d), want (nil, 0)", err, len(metrics))
	}
}
