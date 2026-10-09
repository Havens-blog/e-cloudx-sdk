package types

// Disk 指标 qc_status 数据质量标注取值(与 NAS/OSS 同一套字面量,复用 NASMetricQc*
// 常量作单一来源)。取值是读取侧闭环契约(T8 读取接口原样暴露并映射进 data_status,
// 前端据此渲染警示/异常标记),字面量漂移会让异常行被当正常零使用率。
const (
	// DiskMetricQcOK 数据正常(qc_status 零值)
	DiskMetricQcOK = NASMetricQcOK
	// DiskMetricQcZeroException usage_percent=0 异常行:例外放行落库可见,不拦截不跳过。
	// 注意 0 值语义双关(读取侧结合 usage_scope 甄别):
	//   - 口径缺失 0(厂商无该盘指标/采集不可得,T3 适配器不填假值留 0)——异常行;
	//   - 合法 0(AWS busy_share 全闲盘派生 0.00%,probe-report §1.3 实盘样本)——真闲盘。
	// 写路径统一打标不区分,甄别是 T8 读取侧语义(proposal「qc_status 读取侧闭环」)。
	DiskMetricQcZeroException = NASMetricQcZeroException
)

// Disk 指标 usage_percent 口径标注取值(T1 探测定案,probe-report §2/遗留行动 #3)。
//
// 核心事实:五厂商均无「云盘级容量使用率(used/size)」直接指标,各厂商可得口径
// 语义不同且数值不可直接横比(阿里/华为是空间水位、AWS 是繁忙占比),落库必须带
// 口径标注,前端据此区分呈现(proposal「使用率口径归一(AC-5 定案)」)。
const (
	// DiskUsageScopeCloudDiskLevel 云盘级使用率(预留:tencent/volcengine 二期补后
	// 若实盘收敛出云盘级口径则启用;本期必达三家均不产生该标注)
	DiskUsageScopeCloudDiskLevel = "cloud_disk_level"
	// DiskUsageScopeInstanceLevel 挂载实例级磁盘使用率(aliyun vm.DiskUtilization /
	// huawei SYS.ECS disk_util_inband):维度是实例+挂载点,非单盘容量水位
	DiskUsageScopeInstanceLevel = "instance_level"
	// DiskUsageScopeBusyShare 繁忙时间占比(aws 派生 (1−VolumeIdleTime/窗口)×100):
	// IO 忙闲占比,非容量水位,前端展示须与空间水位区分
	DiskUsageScopeBusyShare = "busy_share"
)

// BytesPerSecToMBPerSec 吞吐单位归一:byte/s → MB/s(1024 进位,与 BytesToGB
// 同一进位口径)。各厂商 Disk 吞吐指标实盘均为 byte/s(probe-report §1.1 阿里
// DiskRead/WriteBPS、§1.2 华为 disk_device_*_bytes_rate;AWS VolumeRead/WriteBytes
// 为 byte/日 Sum,先按窗口秒数归一 byte/s 再经本函数换算),适配器采集边界统一
// 调用本函数,禁止各厂商适配器复制粘贴换算(Hard Rule:单位归一化)。
func BytesPerSecToMBPerSec(raw float64) float64 {
	return raw / (1024 * 1024)
}

// DiskBusySharePercentFromIdle AWS 磁盘使用率派生公式(T1 探测定案,probe-report
// §1.3/§2):usage% = (1 − VolumeIdleTime/窗口秒数) × 100,语义为「繁忙时间占比」
// (IO busy share),非容量水位——落库必须打 DiskUsageScopeBusyShare 标注,前端与
// 空间水位区分呈现。与探测侧 nasprobe.DiskUsagePercentFromIdle 同式(该包为探测
// 专用,生产代码不 import,Hard Rule),生产唯一事实源是本函数。
// 约束:window>0 且 0≤idle≤window 才可计算(否则返回 false——派生不可靠时打标
// 缺失而非伪造,Hard Rule;不产生 NaN/Inf)。
func DiskBusySharePercentFromIdle(idleSeconds, windowSeconds float64) (float64, bool) {
	if windowSeconds <= 0 || idleSeconds < 0 || idleSeconds > windowSeconds {
		return 0, false
	}
	return (1 - idleSeconds/windowSeconds) * 100, true
}

// DiskMetric 云硬盘单日指标(统一格式,由各厂商 DiskMetricQuerier 归一化产出)。
//
// 字段语义(spec:docs/proposals/disk-ops-insight/proposal.md「Proposed Solution」
// 第 1/5 条 + probe-report §2 口径归一):
//   - usage_percent 统一百分比(0~100,厂商口径归一),语义由 UsageScope 标注
//     (见上,五厂商无云盘级容量使用率,必达三家分别为实例级/忙闲占比);
//     0 值统一打 qc_status=zero_exception(DiskMetricQcZeroException 注释);
//   - iops 单位次/秒(当日均值);throughput 单位 MB/s(各厂商原始 byte/s 在
//     适配器采集边界归一,阿里 DiskRead/WriteBPS 与华为 disk_device_*_bytes_rate
//     实盘均为 byte/s,见 probe-report §1.1/§1.2)——无容量字段,「字节 → GB 走
//     共享 types.BytesToGB」对 Disk 不适用(AC「如适用」),模型不设 GB 语义字段;
//   - 时延(读/写时延、P99)与派生 utilization 不落库(Hard Rule:时延留二期,
//     proposal Out of Scope;无容量字段故无派生 utilization 项);
//   - disk_name 冗余落库(Top/趋势展示免联资产表,指标行不随实例删除丢失,
//     与 NASMetric.FsName/OSSMetric.BucketName 同理由);
//   - 唯一键 (account_id, disk_id, date):disk_id 是地域内唯一标识(与 NAS 的
//     fs_id 同型,Disk 是地域性资源),但同一磁盘可被多家云账号纳管(共享盘),
//     唯一键必须含 account_id——(disk_id, date) 会让后写账号覆盖先写账号的行
//     (3cea6d7 多账号修复经验,Hard Rule);
//   - date 为 YYYY-MM-DD(Asia/Shanghai 运营时区自然日)。
type DiskMetric struct {
	DiskID       string  `json:"disk_id" bson:"disk_id"`             // 云盘 ID(唯一键组成部分,地域内唯一)
	DiskName     string  `json:"disk_name" bson:"disk_name"`         // 云盘名称(冗余落库,Top/趋势展示免联资产表)
	Date         string  `json:"date" bson:"date"`                   // YYYY-MM-DD(Asia/Shanghai 运营时区自然日)
	UsagePercent float64 `json:"usage_percent" bson:"usage_percent"` // 使用率(百分比 0~100,口径见 usage_scope);0=打 zero_exception 标(见常量注释)
	UsageScope   string  `json:"usage_scope" bson:"usage_scope"`     // 口径标注:cloud_disk_level / instance_level / busy_share(空=厂商未提供使用率)
	IOPS         float64 `json:"iops" bson:"iops"`                   // IOPS(次/秒,当日均值)
	Throughput   float64 `json:"throughput" bson:"throughput"`       // 吞吐(MB/s,当日均值;适配器边界完成 byte/s→MB/s 归一)
	QcStatus     string  `json:"qc_status" bson:"qc_status"`         // 数据质量标注:空=正常(DiskMetricQcOK);zero_exception=usage_percent=0 异常行
	AccountID    int64   `json:"account_id" bson:"account_id"`       // 云账号 ID(唯一键组成部分:多账号同盘并存各留一行)
	Provider     string  `json:"provider" bson:"provider"`           // 云厂商标识
}
