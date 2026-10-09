package aliyun

import (
	"reflect"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// 聚合维度白名单:字典字段映射列在索引并集内才收录;未收录的字典字段(多列
// 拼接/归一语义,如 DCDN referer、cache_hit 除外——后者走 dimGroupExpr)
// 剔除;dimGroupExpr 维度表达式字段直接收录。目录覆盖三个日志类型。
func TestAggregatableFieldsFromIndexes(t *testing.T) {
	tests := []struct {
		name    string
		logType logquery.LogType
		indexed map[string]bool
		want    []string
	}{
		{
			// DCDN 索引含 uri/request_time:url、latency_ms 可用;response_size/
			// UA/via_info 未索引不出现;cache_hit 走 dimGroupExpr(hit_info)收录。
			name:    "cdn:可聚合字典字段 + dimGroupExpr 字段",
			logType: logquery.LogTypeCDN,
			indexed: map[string]bool{
				"domain": true, "return_code": true, "method": true, "client_ip": true,
				"uri": true, "request_time": true,
			},
			want: []string{
				"cache_hit", "client_ip", "host", "latency_ms", "method", "status", "uri", "url",
			},
		},
		{
			// WAF3 + Akamai:host/status/method/uri/client_ip/rule_name 可用;
			// user_agent(http_user_agent)/geo(region) 未索引不出现;rule_id(cs1)
			// 未索引不出现;action/severity 走 dimGroupExpr 收录。
			name:    "waf:规则名/动作/严重度可用",
			logType: logquery.LogTypeWAF,
			indexed: map[string]bool{
				"host": true, "status": true, "request_method": true,
				"request_path": true, "real_client_ip": true, "name": true, "dhost": true,
			},
			want: []string{
				"action", "client_ip", "host", "method", "rule_name", "severity", "status", "uri",
			},
		},
		{
			// ALB 索引全:url/bytes_sent/tls_protocol 收录;latency 类走 dimGroupExpr。
			name:    "slb:延迟/字节/URL/TLS 全部可聚合",
			logType: logquery.LogTypeSLB,
			indexed: map[string]bool{
				"http_host": true, "status": true, "request_method": true,
				"request_uri": true, "client_ip": true, "upstream_status": true,
				"body_bytes_sent": true, "ssl_protocol": true,
			},
			want: []string{
				"bytes_sent", "client_ip", "host", "latency_ms", "method", "status",
				"tls_protocol", "upstream_latency_ms", "upstream_status", "uri", "url",
			},
		},
		{
			// 索引并集为空:裸列全剔除,dimGroupExpr/函数表达式字段仍收录
			// (真实函数在 len(indexed)==0 时提前返回 nil,由前端回退全量字典)。
			name:    "空索引:仅表达式字段收录",
			logType: logquery.LogTypeCDN,
			indexed: map[string]bool{},
			want:    []string{"cache_hit", "host"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregatableFieldsFromIndexes(tt.logType, tt.indexed)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("aggregatableFieldsFromIndexes() = %v, want %v", got, tt.want)
			}
		})
	}
}
