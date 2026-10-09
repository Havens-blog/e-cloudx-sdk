// Raw 字段裁剪:日志明细的 raw 兜底字段里,头/cookie 全量转储体量极大但
// 只服务明细抽屉的「原始 JSON」展示(列表 Table 列均已单独抽取)。WAF 响应体
// 实测 request_headers_all 占 36%、http_cookie 14%、response_header 9%,
// 加上 AWS httpRequest/Cookie 与 Akamai CEF 头,累计约 70%。裁剪后响应
// 12MB→~4MB,前端首屏下载与 JSON 解析显著变快(明细抽屉少原始头字段)。
package logquery

// rawHeaderDumpKeys 需裁剪的头/cookie 转储字段名(按源分组注释)。
var rawHeaderDumpKeys = map[string]bool{
	"request_headers_all":       true, // aliyun WAF3 全量请求头 JSON
	"http_cookie":               true, // aliyun WAF3 全量 Cookie
	"response_header":           true, // aliyun WAF3 全量响应头 JSON
	"httpRequest":               true, // AWS WAF 完整请求对象(含 headers/cookie)
	"Cookie":                    true, // AWS WAF 全量 Cookie
	"AkamaiSiemRequestHeaders":  true, // Akamai 自采 CEF 请求头转储
	"AkamaiSiemResponseHeaders": true, // Akamai 自采 CEF 响应头转储
}

// IsRawHeaderDumpKey 判断字段名是否属于被裁剪的头/cookie 转储(供测试与
// 调用方断言「除裁剪集外其余字段全保留」)。
func IsRawHeaderDumpKey(k string) bool { return rawHeaderDumpKeys[k] }

// TrimRawHeaderDump 就地删除原始字段 map 里的头/cookie 转储,返回是否有删。
// 调用方传入的 map 为每条目新建(无共享),就地修改安全;须在 typed 字段抽取
// **之后**调用(如 AWS httpRequest 先解 ClientIP/URI 再裁)。
func TrimRawHeaderDump(raw map[string]any) bool {
	changed := false
	for k := range rawHeaderDumpKeys {
		if _, ok := raw[k]; ok {
			delete(raw, k)
			changed = true
		}
	}
	return changed
}