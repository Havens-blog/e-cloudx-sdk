package types

// BytesToGB 字节 → GB(二进制 GiB)。
// 规格口径(proposal「单位归一化与字段语义」):各厂商 NASMetricQuerier 必须在
// 采集边界完成「厂商原始字节 → GB」换算,禁止把字节直接写进 GB 字段。
// NAS 指标链路(T3 三厂商适配器 + T4 执行器/回填)统一复用本函数,
// 不在适配器内各自手写分母(nasprobe 包内同义函数为探测期产物,委托至此)。
func BytesToGB(raw float64) float64 {
	return raw / (1024 * 1024 * 1024)
}

// NAS 指标 qc_status 数据质量标注取值(spec「单位归一化与字段语义」)。
// 取值是读取侧闭环契约(读取接口原样暴露并映射进 data_status,前端据此
// 渲染警示/异常标记),字面量漂移会让异常行被当正常零容量。
const (
	// NASMetricQcOK 数据正常(qc_status 零值)
	NASMetricQcOK = ""
	// NASMetricQcZeroException capacity=0 异常行:例外放行落库可见,不拦截不跳过
	// (华为/AWS 实盘现状即 capacity=0;不继承 CDN「全零跳过」过滤,否则首日整表静默为空)
	NASMetricQcZeroException = "zero_exception"
)

// NASMetric NAS 文件系统单日容量指标(统一格式,由各厂商 NASMetricQuerier 归一化产出)。
//
// 字段语义(spec:docs/proposals/nas-ops-insight/proposal.md「单位归一化与字段语义」):
//   - capacity / used_capacity 单位为 GB(二进制 GiB),各适配器必须在采集边界完成
//     「厂商原始字节 → GB」换算,禁止把字节直接写进 GB 字段(现行 sync_nas.go 把
//     原始字节/失真值直写 GB 语义字段是数据质量 bug 根源,新链路不得复刻);
//     DAO 写入路径另做 [1MB, 1PB] 数量级自检,capacity=0 例外放行打
//     qc_status=zero_exception(Hard Rule:单位必须 GB;零值行落库可见);
//   - utilization 不落库(Hard Rule:避免重采时 capacity/used/utilization 三字段
//     不一致),读取时由 used_capacity/capacity 派生;capacity=0 时 utilization
//     记空值,不写 NaN;
//   - 与 NASInstance.Capacity(int64 整数 GB,名义容量)不同,指标值用 float64:
//     数量级自检下界 1MB(=1/1024 GiB)与探测实测值(如 1378.14 GB)需要小数精度;
type NASMetric struct {
	FsID         string  `json:"fs_id" bson:"fs_id"`                 // 文件系统 ID(唯一键组成部分)
	FsName       string  `json:"fs_name" bson:"fs_name"`             // 文件系统名称(冗余落库,Top/趋势展示免联资产表,指标行不随实例删除丢失)
	Date         string  `json:"date" bson:"date"`                   // YYYY-MM-DD(Asia/Shanghai 运营时区自然日)
	Capacity     float64 `json:"capacity" bson:"capacity"`           // 总容量(GB,二进制 GiB);0=异常行(见 qc_status)
	UsedCapacity float64 `json:"used_capacity" bson:"used_capacity"` // 已用容量(GB,二进制 GiB)
	QcStatus     string  `json:"qc_status" bson:"qc_status"`         // 数据质量标注:空=正常(NASMetricQcOK);zero_exception=capacity=0 异常行
	AccountID    int64   `json:"account_id" bson:"account_id"`       // 云账号 ID(唯一键组成部分:多账号同 fs 并存各留一行)
	Provider     string  `json:"provider" bson:"provider"`           // 云厂商标识
}
