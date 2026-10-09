package types

// RDS 指标 qc_status 数据质量标注取值(与 NAS/OSS/Disk 同一套字面量,复用
// NASMetricQc* 常量作单一来源)。取值是读取侧闭环契约(T8 读取接口原样暴露并
// 映射进 data_status,前端据此渲染警示/异常标记),字面量漂移会让异常行被当
// 正常零负载。
const (
	// RDSMetricQcOK 数据正常(qc_status 零值)
	RDSMetricQcOK = NASMetricQcOK
	// RDSMetricQcZeroException 四指标全 0 异常行(CPU/内存/磁盘/连接):例外放行
	// 落库可见,不拦截不跳过(proposal Key Scenarios「qc_status 异常」+ T5 执行器
	// 「不继承 CDN 全零过滤」+ T8 读取闭环「四指标全 0 原样暴露」)。注意全 0 形态
	// 双关(读取侧结合实例 Status 甄别):
	//   - 停用/重启中实例指标全 0(真停机,T8 打 data_status 而非按异常呈现);
	//   - 厂商指标全缺/采集不可得(T3 适配器不填假值留 0)——异常行。
	// 写路径统一打标不区分,甄别是 T8 读取侧语义(proposal「qc_status 读取侧闭环」)。
	RDSMetricQcZeroException = NASMetricQcZeroException
)

// RDSMetric 云数据库单日指标(统一格式,由各厂商 RDSMetricQuerier 归一化产出)。
//
// 字段语义(spec:docs/proposals/rds-ops-insight/proposal.md「Proposed Solution」
// 第 1/5 条 + probe-report §2 口径归一定案):
//   - cpu_percent/memory_percent/disk_percent 统一百分比 0~100(厂商口径归一,
//     probe-report §2:aliyun/huawei % 直给,aws FreeableMemory/FreeStorageSpace
//     字节换算,公式实盘验证 5/5 PASS);任一为 0 不单独打标,四指标全 0 才是
//     zero_exception(低负载单 0 是正常业务事实,probe-report 零值 13 行);
//   - connections 保留绝对值(个,非使用率)——连接耗尽是数据库雪崩前兆,
//     绝对值才能判断逼近 max_connections;注意厂商侧连接类指标有使用率口径
//     (aliyun ConnectionUsage),T3 适配器须换算为绝对值或换算留痕,禁止混写;
//   - engine 元数据落库:T1 探测定案 proposal「engine 仅透传」假设不成立——
//     aliyun 指标名按引擎前缀分派(SQLServer_*)、huawei 维度键按引擎分键
//     (rds_cluster_id/postgresql_cluster_id),engine 是适配器查询分派的必经
//     依据,落库供 Top/趋势展示与后续引擎口径追溯(probe-report §2);
//   - instance_name 冗余落库(Top/趋势展示免联资产表,指标行不随实例删除丢失,
//     与 DiskMetric.DiskName/NASMetric.FsName 同理由);
//   - 不落库 QPS/IOPS 吞吐与派生值(Hard Rule:吞吐型指标留二期,proposal
//     Out of Scope;适用时读取侧派生);
//   - 唯一键 (account_id, rds_id, date):rds_id 是地域内唯一标识(与 NAS 的
//     fs_id/Disk 的 disk_id 同型,RDS 是地域性资源),但同一实例可被多家云账号
//     纳管(跨账号共享数据库),唯一键必须含 account_id——(rds_id, date) 会让
//     后写账号覆盖先写账号的行(3cea6d7 多账号修复经验,Hard Rule);
//   - date 为 YYYY-MM-DD(Asia/Shanghai 运营时区自然日)。
type RDSMetric struct {
	RdsID         string  `json:"rds_id" bson:"rds_id"`                 // RDS 实例 ID(唯一键组成部分,地域内唯一)
	InstanceName  string  `json:"instance_name" bson:"instance_name"`   // 实例名称(冗余落库,Top/趋势展示免联资产表)
	Date          string  `json:"date" bson:"date"`                     // YYYY-MM-DD(Asia/Shanghai 运营时区自然日)
	CPUPercent    float64 `json:"cpu_percent" bson:"cpu_percent"`       // CPU 使用率(百分比 0~100)
	MemoryPercent float64 `json:"memory_percent" bson:"memory_percent"` // 内存使用率(百分比 0~100;aws 由 FreeableMemory 换算,T3 边界完成)
	DiskPercent   float64 `json:"disk_percent" bson:"disk_percent"`     // 磁盘使用率(百分比 0~100;aws 由 FreeStorageSpace 换算,T3 边界完成)
	Connections   int64   `json:"connections" bson:"connections"`       // 连接数(个,绝对值非使用率)
	Engine        string  `json:"engine" bson:"engine"`                 // 数据库引擎(mysql/postgresql/mariadb/sqlserver;适配器分派依据+展示元数据,见类型注释)
	QcStatus      string  `json:"qc_status" bson:"qc_status"`           // 数据质量标注:空=正常(RDSMetricQcOK);zero_exception=四指标全 0 异常行
	AccountID     int64   `json:"account_id" bson:"account_id"`         // 云账号 ID(唯一键组成部分:跨账号共享实例各留一行)
	Provider      string  `json:"provider" bson:"provider"`             // 云厂商标识
}
