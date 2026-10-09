package types

// OSS 指标 qc_status 数据质量标注取值(与 NAS 同一套字面量,复用 NASMetricQc*
// 常量作单一来源)。取值是读取侧闭环契约(T10 读取接口原样暴露并映射进
// data_status,前端据此渲染警示/异常标记),字面量漂移会让异常行被当正常零容量。
const (
	// OSSMetricQcOK 数据正常(qc_status 零值)
	OSSMetricQcOK = NASMetricQcOK
	// OSSMetricQcZeroException storage_size=0 异常行:例外放行落库可见,不拦截不跳过
	// (实盘空桶/未开通统计的桶容量即 0;不继承「全零跳过」过滤,否则首日整表静默为空)
	OSSMetricQcZeroException = NASMetricQcZeroException
)

// OSSMetric OSS 存储桶单日容量指标(统一格式,由各厂商 OSSMetricQuerier 归一化产出)。
//
// 字段语义(proposal「Proposed Solution」第 1/5 条,规格:docs/proposals/oss-ops-insight/proposal.md):
//   - storage_size 单位为 GB(二进制 GiB),各适配器必须在采集边界完成
//     「厂商原始字节 → GB」换算(共享 types.BytesToGB,禁止把字节直接写进 GB 字段);
//     DAO 写入路径另做 [1MB, 1PB] 数量级自检,storage_size=0 例外放行打
//     qc_status=zero_exception(Hard Rule:单位必须 GB;零值行落库可见);
//   - object_count 为桶内对象数(整数);
//   - 分层大小(Standard/IA/Archive/ColdArchive)与 utilization 均不落库
//     (Hard Rule:分层大小留二期;utilization 读取时由容量派生);
//   - OSS 是全局服务(ListBuckets region 可选,各厂商实现有 defaultRegion 回退),
//     与 CDN 同型:Querier 签名无 region,唯一键用 bucket_name(全局唯一)类比
//     CDN 的 domain;bucket_name 冗余落库,Top/趋势展示免联资产表,指标行不随
//     实例删除丢失;
//   - 唯一键 (account_id, bucket_name, date):bucket_name 全局唯一,但同一 bucket
//     可被多家云账号纳管(跨账号共享存储),唯一键必须含 account_id——(bucket_name,
//     date) 会让后写的账号覆盖先写账号的行(3cea6d7 多账号修复经验);
//   - date 为 YYYY-MM-DD(Asia/Shanghai 运营时区自然日)。
type OSSMetric struct {
	BucketName  string  `json:"bucket_name" bson:"bucket_name"`   // 存储桶名称(唯一键组成部分)
	Date        string  `json:"date" bson:"date"`                 // YYYY-MM-DD(Asia/Shanghai 运营时区自然日)
	StorageSize float64 `json:"storage_size" bson:"storage_size"` // 存储量(GB,二进制 GiB);0=异常行(见 qc_status)
	ObjectCount int64   `json:"object_count" bson:"object_count"` // 对象数量
	QcStatus    string  `json:"qc_status" bson:"qc_status"`       // 数据质量标注:空=正常(OSSMetricQcOK);zero_exception=storage_size=0 异常行
	AccountID   int64   `json:"account_id" bson:"account_id"`     // 云账号 ID(唯一键组成部分:多账号同 bucket 名并存各留一行)
	Provider    string  `json:"provider" bson:"provider"`         // 云厂商标识
}

// MBToGB 兆字节(MB,厂商监控口径,如 tencent QCE/COS StdStorage)→ GB(二进制 GiB)。
// probe-report §1.4/遗留行动 #4 定案:tencent 容量单位是 **MB 不是 byte**,禁止把
// MB 当 byte 直接进 BytesToGB(会缩小 1024^2 倍);本函数为厂商 MB 口径的共享换算
// 入口,内部委托 BytesToGB(MB→byte → byte→GB 单一换算链,不在适配器内复制粘贴分母)。
func MBToGB(mb float64) float64 {
	return BytesToGB(mb * 1024 * 1024)
}
