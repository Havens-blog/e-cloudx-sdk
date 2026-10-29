package types

// DNSDomain 云厂商托管域名
type DNSDomain struct {
	DomainID    string `json:"domain_id"`    // 云厂商域名 ID（如 Route53 的 HostedZoneId）
	DomainName  string `json:"domain_name"`  // 域名，如 example.com
	RecordCount int64  `json:"record_count"` // 解析记录数
	Status      string `json:"status"`       // 域名状态：normal / paused / locked
}

// DNSRecord 云厂商解析记录
type DNSRecord struct {
	RecordID string `json:"record_id"` // 云厂商记录 ID
	Domain   string `json:"domain"`    // 所属域名
	RR       string `json:"rr"`        // 主机记录，如 www、@、mail
	Type     string `json:"type"`      // 记录类型：A / AAAA / CNAME / MX / TXT / NS / SRV / CAA
	Value    string `json:"value"`     // 记录值
	TTL      int    `json:"ttl"`       // TTL（秒）
	Priority int    `json:"priority"`  // MX 优先级（仅 MX 类型有效）
	Line     string `json:"line"`      // 线路类型：default / telecom / unicom / mobile 等
	Status   string `json:"status"`    // 记录状态：enable / disable
	// Weight 权重（负载均衡解析，通常 0-100）。
	// 指针语义：nil = 该云/该记录无权重概念或云 API 未返回（上层展示为 "-"）；
	// 非 nil = 云 API 明确返回的权重值。目前仅阿里云云解析返回（DescribeDomainRecords.Record.Weight）。
	Weight *int `json:"weight,omitempty"`
}

// CreateDNSRecordRequest 创建解析记录请求
type CreateDNSRecordRequest struct {
	RR       string `json:"rr" binding:"required"`
	Type     string `json:"type" binding:"required"`
	Value    string `json:"value" binding:"required"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
	Line     string `json:"line"`
	// Weight 权重：nil = 不设置；仅对 DNSWeightWritable 返回 true 的云可写。
	Weight *int `json:"weight,omitempty"`
}

// UpdateDNSRecordRequest 修改解析记录请求
type UpdateDNSRecordRequest struct {
	RR       string `json:"rr"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
	Line     string `json:"line"`
	// Weight 权重：nil = 不修改；仅对 DNSWeightWritable 返回 true 的云可写。
	Weight *int `json:"weight,omitempty"`
}

// DNSWeightWritable 该云厂商的 DNS 解析记录权重**是否可经当前适配器写入**。
//
// 事实依据（2026-10-29 核对各云 vendored SDK）：
//   - aliyun：DescribeDomainRecords 返回 Record.Weight（可读），但
//     AddDomainRecord/UpdateDomainRecord 请求结构体**没有 Weight 参数** → 只读；
//   - huawei：使用 CreateRecordSet（无权重概念）→ 不支持；
//   - volcano：DNS 创建/更新未实现 → 不支持。
//
// 尚未核实写能力的云（tencent/aws）暂返回 false，核实后在实现读写的同一提交内放宽，
// 避免前端出现"能填但推不上去"的假表单。
func DNSWeightWritable(provider string) bool {
	switch provider {
	case "tencent", "aws":
		// 占位待核实：核实到可写后改 true 并补适配器实现。
		return false
	default:
		return false
	}
}

// DNSWeightSupported 该云厂商的 DNS 记录**是否可能带权重**（读取侧，用于前端展示口径）。
// aliyun 可读（负载均衡记录）；其余暂按不支持处理。
func DNSWeightSupported(provider string) bool {
	switch provider {
	case "aliyun":
		return true
	default:
		return DNSWeightWritable(provider)
	}
}

// NewDNSWeight 便捷构造权重指针（适配器把云端返回值映射为 *int）。
// v <= 0 返回 nil：云 API 对"无权重概念"的记录一律返回 0，用 nil 表达"无值"，
// 避免上层把"不支持"展示成假的 0。
func NewDNSWeight(v int) *int {
	if v <= 0 {
		return nil
	}
	w := v
	return &w
}
