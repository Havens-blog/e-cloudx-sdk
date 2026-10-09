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
}

// CreateDNSRecordRequest 创建解析记录请求
type CreateDNSRecordRequest struct {
	RR       string `json:"rr" binding:"required"`
	Type     string `json:"type" binding:"required"`
	Value    string `json:"value" binding:"required"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
	Line     string `json:"line"`
}

// UpdateDNSRecordRequest 修改解析记录请求
type UpdateDNSRecordRequest struct {
	RR       string `json:"rr"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
	Line     string `json:"line"`
}
