package volcano

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
)

// volcano OSS 指标适配器(OSSMetricQuerier 尽力而为厂商之一,「二期补」)。
//
// 本适配器当前为探测不支持桩实现——不建立真实查询路径,也不假装有数据。
//
// 判定来源:probe-report §1.5/§3/§4(docs/features/oss-ops-insight/probe-report.md,
// 2026-09-20 运行时实测,只读凭证):
//   - cloudmonitor 网关仅 GetMetricData(Version 2018-01-01)注册;
//     ListNamespaces/ListMetrics 一律 InvalidActionOrVersion(与 NAS 前案一致);
//   - 真实 bucket(ark-auto-2100700292-cn-beijing-default,cn-beijing)+ 静态
//     候选矩阵(Namespace TOS/Volcano_TOS/Vulcan_TOS/VEI_TOS × SubNamespace ×
//     指标名 × 维度名)共 289 组合全部 metric not found;阳性对照
//     (Vulcan_ECS/Instance/CPUPercent)同样 not found —— 根因:账号「云产品
//     监控指标订阅」未开通(文档 6408/114674),指标名无法经实盘收敛;
//   - 升格判定:bucket 数占比 15.58%(>15% 贴线超阈)但探测不可用 → 按 spec
//     显式降级「二期补」,发布说明承诺补采窗口,不触发升格。
//
// 尽力而为语义(Hard Rule):探测不支持返回空切片 + nil error,打 INFO 不打
// ERROR,不假装有数据。
//
// 二期补重试路径(probe-report §1.5 固化):
//  1. 开通云产品监控指标订阅(写操作,不在采集链路范围);
//  2. 重跑 internal/shared/cloudx/volcano/oss_probe_manual_test.go 的
//     TestManualProbeVolcanoTOSCloudMonitor 收敛指标名,并重算 probe-report
//     §3 分布表(bucket 数占比 15.58% 贴线超阈,按真实容量重算可能明显变化);
//  3. 按文档候选定案 Namespace=TOS、SubNamespace=tos、MetricName=BucketSize|
//     StorageSize、Dimension=BucketName,把本桩替换为真实 GetMetricData 查询
//     (Period 必须为时长字符串如 "1h",字节 → GB 换算用 types.BytesToGB)。

var _ cloudx.OSSMetricQuerier = (*TOSAdapter)(nil)

// GetOSSMetrics 火山引擎 OSS(TOS)指标采集暂未启用(探测不支持,二期补):
// 返回空切片 + nil error,INFO 级日志标注原因(不报 ERROR,不触发失败计数)。
func (a *TOSAdapter) GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error) {
	a.logger.Info("火山引擎OSS指标采集暂未启用(探测不支持,二期补)",
		elog.String("bucket", bucketName),
		elog.String("provider", "volcano"),
		elog.String("reason", "cloudmonitor 指标注册表为空(云产品监控指标订阅未开通),指标名未收敛,见 probe-report §1.5"))
	return []types.OSSMetric{}, nil
}
