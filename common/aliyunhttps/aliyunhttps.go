// Package aliyunhttps 统一阿里云通用 SDK 客户端的传输协议选择。
//
// 通用 SDK（github.com/aliyun/alibaba-cloud-sdk-go）的 sdk.NewConfig() 把 Scheme 默认成
// "HTTP"，而 NewClientWithAccessKey 这类简写构造器不会修改它。构建 RPC 请求时若
// request.Scheme 为空，SDK 会取用该默认值（sdk/client.go 的 ProcessCommonRequest ->
// requests/rpc_request.go 的 "%s://%s" 拼接），于是未显式设置协议的调用路径会把
// AccessKeyId 与签名发到明文 HTTP —— 与 #26 修的 OSS 问题（SDK 对无 scheme 的 endpoint
// 兜底成 http）同源。
//
// 这里集中提供「构造完客户端后强制 HTTPS」的入口，避免每个适配器各写一遍。
package aliyunhttps

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
)

// SchemeHTTPS 阿里云 SDK 拼 URL 时接受的协议名（SDK 内部会 ToLower 后再拼接）。
const SchemeHTTPS = "HTTPS"

// Force 把客户端配置的默认协议改成 HTTPS。
//
// 只改默认值：调用点显式设置的 request.Scheme 优先级更高，不会被覆盖。
// 传入 nil（客户端未初始化）时什么都不做。
func Force(config *sdk.Config) {
	if config == nil {
		return
	}
	config.Scheme = SchemeHTTPS
}
