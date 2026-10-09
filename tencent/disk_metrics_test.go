package tencent

import (
	"context"
	"testing"

	"github.com/gotomicro/ego/core/elog"
)

// TestGetDiskMetricsProbeUnsupported 探测不支持路径(AC:尽力而为失败双路径之
// 「探测不支持」):T1 定案「二期补」(disk-ops-insight probe-report §1.4/§3/§4,
// 2026-09-21 运行时实测,只读凭证——QCE/CBS 仅注册快照类 2 指标,云盘 IO/使用率
// 指标全部未注册,云监控数据查询权限未开通;磁盘数占比 0.49%、容量占比 0.36%,
// 双口径均 ≤15% 且探测不可用,不触发升格)——必须返回空切片 + nil error,不报
// ERROR、不假装有数据。INFO 级日志由适配器发出(尽力而为语义,失败计数不受影响)。
func TestGetDiskMetricsProbeUnsupported(t *testing.T) {
	adapter := NewDiskAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)

	metrics, err := adapter.GetDiskMetrics(context.Background(), "disk-1", "data-disk-1", "ap-guangzhou", "2026-09-19", "2026-09-20")
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
