package cloudx

import "encoding/pem"

// GetCert PEM 通道净化公共实现（cert-cloud-discovery-import 任务 1）。
// 四云 GetCert 扩展暴露的 PEM 定义为"仅 CERTIFICATE 块的净化序列"（叶在前
// fullchain 口径），净化为构造性保证（块级过滤实现于适配层），不依赖调用方约定。

// SanitizeCertChainPEM 构造性净化云侧 PEM 材料：逐块解码输入，仅保留
// CERTIFICATE 块并按输入原序重新编码拼装（叶在前口径由调用方的输入顺序保证：
// AWS 为 GetCertificate 的 Certificate+CertificateChain 拼接，其余云按云侧
// 返回形态），PRIVATE KEY / PKCS#8 / PKCS#12 等任何非 CERTIFICATE 内容一律
// 在净化中被丢弃。输入为空或不含 CERTIFICATE 块时返回空串。
func SanitizeCertChainPEM(raw []byte) string {
	var out []byte
	rest := raw
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			continue
		}
		out = append(out, pem.EncodeToMemory(block)...)
	}
	return string(out)
}

// Zeroize 尽力清除敏感 buffer 内容（GetCert 净化前的云侧原始材料用后清除，
// 尤其 Azure KeyVault secret 全量值——exportable 密钥策略下可能含私钥 bundle）。
// Go string 不可变，仅能保证本层持有的字节副本被覆盖归零。
func Zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
