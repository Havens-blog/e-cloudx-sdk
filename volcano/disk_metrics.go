package volcano

import (
	"context"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
)

// volcano Disk 指标适配器(DiskMetricQuerier 尽力而为厂商之一,「二期补」)。
//
// 本适配器当前为探测不支持桩实现——不建立真实查询路径,也不假装有数据。
//
// 判定来源:probe-report §1.5/§3/§4(docs/features/disk-ops-insight/probe-report.md,
// 2026-09-21 运行时实测,只读凭证):
//   - 指标查询:真实磁盘(vol-3xccls…,50GB)+ 静态候选矩阵(Namespace
//     Vulcan_EBS/Volcano_EBS/VEI_EBS/EBS/Vulcan_Storage_EBS × SubNamespace
//     ebs/volume/disk/block_storage × 指标名 13 个容量/IO/使用率候选 × 维度名
//     volume_id/disk_id/VolumeId/DiskId)共 2240 组合全部 metric not found;
//     阳性对照(真实 ECS 指标 Vulcan_ECS/Instance/CPUPercent)同样 not found
//     —— 根因:账号云产品监控指标注册表为空(云产品监控指标需「产品订阅」
//     开通,文档 6408/114674),与 NAS/OSS 前案同根因;
//   - 升格判定:磁盘数占比 50.96%、容量占比 46.33% 均 >15% 但探测不可用 →
//     按 spec 显式降级「二期补」,发布说明承诺二期补采窗口,不触发升格
//     (probe-report §3 升格判定表)。
//
// 尽力而为语义(Hard Rule):探测不支持返回空切片 + nil error,打 INFO 不打
// ERROR,不假装有数据(不阻塞全流程,失败计数不受影响)。
//
// 二期补重试路径(probe-report §1.5 固化;磁盘占比 >50%,二期补优先级最高):
//  1. 开通云产品监控指标订阅(写操作,不在采集链路范围);
//  2. 重跑 internal/shared/cloudx/volcano/disk_probe_manual_test.go 的
//     TestManualProbeVolcanoEBSCloudMonitor 收敛指标名(2240 组合候选矩阵已在
//     脚本),并重算 probe-report §3 分布表;
//  3. 按文档候选把本桩替换为真实 cloudmonitor GetMetricData 查询(Period 为
//     时长字符串如 "1h",参照 nas_metrics.go 桩注释先例),并补齐失败三分路径
//     单测(调用失败→ERROR+error;真实无数据点→空+nil):IOPS 原始单位次/秒
//     直取;吞吐 byte/s → MB/s 换算走共享 types.BytesPerSecToMBPerSec
//     (Hard Rule 单位归一化,禁复制粘贴,与 T3 三厂商共用)。
//
// region:云盘为地域性资源,二期补落地时监控客户端必须按实例真实 region 创建,
// 不做全局 region 推断(interfaces.go DiskMetricQuerier 契约)。

var _ cloudx.DiskMetricQuerier = (*DiskAdapter)(nil)

// GetDiskMetrics 火山引擎磁盘指标采集暂未启用(探测不支持,二期补):
// 返回空切片 + nil error,INFO 级日志标注原因(不报 ERROR,不触发失败计数)。
func (a *DiskAdapter) GetDiskMetrics(ctx context.Context, diskID, diskName, region, startDate, endDate string) ([]types.DiskMetric, error) {
	a.logger.Info("火山引擎磁盘指标采集暂未启用(探测不支持,二期补)",
		elog.String("disk_id", diskID),
		elog.String("region", region),
		elog.String("provider", "volcano"),
		elog.String("reason", "cloudmonitor 指标注册表为空(云产品监控指标订阅未开通),2240 组合候选矩阵 metric not found,指标名未收敛,见 disk-ops-insight probe-report §1.5"))
	return []types.DiskMetric{}, nil
}
