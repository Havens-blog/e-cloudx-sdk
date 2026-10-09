package tencent

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
)

// tencent Disk 指标适配器(DiskMetricQuerier 尽力而为厂商之一,「二期补」)。
//
// 本适配器当前为探测不支持桩实现——不建立真实查询路径,也不假装有数据。
//
// 判定来源:probe-report §1.4/§3/§4(docs/features/disk-ops-insight/probe-report.md,
// 2026-09-21 运行时实测,只读凭证):
//   - 元数据接口 DescribeBaseMetrics(QCE/CBS)仅返回 2 个快照类指标
//     (SnapshotCapacityUsage/SnapshotCountUsage,dims 为空),云盘 IO/使用率
//     指标全部未注册;
//   - 指标查询 8 个文档口径候选(DiskReadIops/DiskWriteIops/DiskReadTotal/
//     DiskWriteTotal/DiskIoActiveTimePercent/DiskTotalIoRatio/DiskUsage/
//     CvmDiskUsage)× 5 实盘盘(维度 diskId,与文档一致)全部
//     InvalidParameterValue: namespace or metricName invalid;阳性对照
//     QCE/CVM CpuUsage 同样被拒 —— 根因:账号云监控数据查询权限/云盘指标
//     订阅未开通,指标名无法经实盘收敛;
//   - 升格判定:磁盘数占比 0.49%、容量占比 0.36%,双口径均 ≤15% 且探测不可用
//     → 不触发升格,维持尽力而为 +「二期补」(probe-report §4:T4 不纳入本期)。
//
// 尽力而为语义(Hard Rule):探测不支持返回空切片 + nil error,打 INFO 不打
// ERROR,不假装有数据(不阻塞全流程,失败计数不受影响)。
//
// 二期补重试路径(probe-report §1.4 固化):
//  1. 控制台/子账号开通云监控 GetMonitorData 数据权限与云硬盘基础监控订阅
//     (写操作,不在采集链路范围);
//  2. 重跑 internal/shared/cloudx/tencent/disk_probe_manual_test.go 的
//     TestManualProbeTencentQCECBSMetrics 收敛指标名(8 候选矩阵已在脚本);
//  3. 按文档候选定案(QCE/CBS DiskReadIops/DiskWriteIops/DiskReadTotal/
//     DiskWriteTotal + 维度 diskId,appid 经 CAM GetUserAppId 解析缓存,参照
//     nas_metrics.go resolveAppID 先例)把本桩替换为真实 GetMonitorData 查询,
//     并补齐失败三分路径单测(调用失败→ERROR+error;真实无数据点→空+nil):
//     IOPS 原始单位次/秒直取;吞吐 byte/s → MB/s 换算走共享
//     types.BytesPerSecToMBPerSec(Hard Rule 单位归一化,禁复制粘贴,与 T3
//     三厂商共用)。
//
// region:云盘为地域性资源,二期补落地时监控客户端必须按实例真实 region 创建,
// 不做全局 region 推断(interfaces.go DiskMetricQuerier 契约)。

var _ cloudx.DiskMetricQuerier = (*DiskAdapter)(nil)

// GetDiskMetrics 腾讯云 CBS 磁盘指标采集暂未启用(探测不支持,二期补):
// 返回空切片 + nil error,INFO 级日志标注原因(不报 ERROR,不触发失败计数)。
func (a *DiskAdapter) GetDiskMetrics(ctx context.Context, diskID, diskName, region, startDate, endDate string) ([]types.DiskMetric, error) {
	a.logger.Info("腾讯云磁盘指标采集暂未启用(探测不支持,二期补)",
		elog.String("disk_id", diskID),
		elog.String("region", region),
		elog.String("provider", "tencent"),
		elog.String("reason", "QCE/CBS 云盘 IO/使用率指标未注册且云监控数据查询权限未开通,指标名未收敛,见 disk-ops-insight probe-report §1.4"))
	return []types.DiskMetric{}, nil
}
