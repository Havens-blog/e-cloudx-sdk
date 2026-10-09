package volcano

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
)

// volcano NAS 指标适配器(NASMetricQuerier 尽力而为厂商之一,「二期补」)。
//
// 本适配器当前为探测不支持桩实现——不建立真实查询路径,也不假装有数据。
//
// 判定来源:probe-report §1.4/§3/§4(docs/features/nas-ops-insight/probe-report.md,
// 2026-09-19 运行时实测,只读凭证):
//   - cloudmonitor 网关(cloudmonitor.<region>.volcengineapi.com)仅
//     GetMetricData(Version 2018-01-01)注册;ListNamespaces/ListMetrics/
//     QueryMetricData 在该网关一律 InvalidActionOrVersion;
//   - 指标查询 355+ 候选组合(Namespace × SubNamespace × 指标名 × 维度名)
//     全部 metric not found,阳性对照(Vulcan_ECS/Instance/CPUPercent)同样
//     not found —— 根因:账号「云产品监控指标订阅」未开通(文档 6408/114674),
//     指标名无法经实盘收敛;
//   - 升格判定:实例数占比 12.6%、容量原值占比 14.40%,双口径均 ≤15% 且
//     探测不可用 → 不触发升格,维持尽力而为 +「二期补」。
//
// 尽力而为语义(Hard Rule):探测不支持返回空切片 + nil error,打 INFO 不打
// ERROR,不假装有数据(AC-2/AC-5)。
//
// 二期补重试路径(probe-report §1.4 固化):
//  1. 开通云产品监控指标订阅(写操作,不在采集链路范围);
//  2. 重跑 internal/shared/cloudx/volcano/probe_manual_test.go 的
//     TestManualProbeVolcanoNASCloudMonitor 收敛指标名;
//  3. 按文档候选定案 Namespace=FileNAS、MetricName=UsedCapacity/TotalCapacity/
//     CapacityUsage、Dimension=FileSystemId,把本桩替换为真实 GetMetricData
//     查询(Period 必须为时长字符串如 "1h",字节 → GB 换算用 types.BytesToGB)。

var _ cloudx.NASMetricQuerier = (*NASAdapter)(nil)

// GetNASMetrics 火山引擎 NAS 指标采集暂未启用(探测不支持,二期补):
// 返回空切片 + nil error,INFO 级日志标注原因(不报 ERROR,不触发失败计数)。
func (a *NASAdapter) GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error) {
	a.logger.Info("火山引擎NAS指标采集暂未启用(探测不支持,二期补)",
		elog.String("fs_id", fsID),
		elog.String("region", region),
		elog.String("provider", "volcano"),
		elog.String("reason", "cloudmonitor 指标注册表为空(云产品监控指标订阅未开通),指标名未收敛,见 probe-report §1.4"))
	return []types.NASMetric{}, nil
}
