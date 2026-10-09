package volcano

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volcengine-go-sdk/service/certificateservice"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
	"github.com/volcengine/volcengine-go-sdk/volcengine/credentials"
	"github.com/volcengine/volcengine-go-sdk/volcengine/request"
	"github.com/volcengine/volcengine-go-sdk/volcengine/session"
)

// 火山引擎证书库只读发现适配（cert-volcano-import-sync 任务 1）：
// certificateservice（volcengine-go-sdk v1.2.9 既有包，无新增依赖）的
// CertificateGetInstanceList（分页枚举）/ CertificateGetInstance（链下载）两个
// 只读 API 封装，供证书发现导入管线枚举在库证书并解析台账要素（指纹/CN/SAN/
// 有效期）。本层仅做单次云 API 调用封装与链解析，不做增量比对、台账写入等
// 业务编排（属 cert 功能域同步服务层）。
//
// Hard Rules（只读纪律与私钥卫生）：
//   - 适配器为纯只读：SDK 窄接口仅含 List/Get 两个读方法（构造性保证，
//     UploadCert/BindResource 等写通路不在接口面内），绝不调用云写方法；
//   - 响应的 PrivateKey 字段（含 EncryptionCertificateDetail 同名字段）从不
//     读取、不进入任何返回类型、不进入日志/错误信息——证书材料仅取 Chain
//     （PEM 列表），且经 SanitizeCertChainPEM 块级净化（构造性保证仅保留
//     CERTIFICATE 块），原始字节副本净化后即刻归零；
//   - 错误信息经 wrapCertCloudErr 统一归一（对齐既有跨云 wrapCertCloudErr
//     语义）：统一云/操作前缀 + %w 透传云侧错误明文，不携带密钥材料。

// 云侧实例状态枚举（SDK 模型 Status：NotSubmitted/Pending/Issued/Cancelling/
// Canceled/Revoking/Revoked/Failed/Unknown），本适配器仅放行已签发。
const (
	// certStatusIssued 已签发（唯一可供导入的实例状态）
	certStatusIssued = "Issued"
	// certStatusRevoked 已撤销（过滤态，显式命名供测试与日志语义）
	certStatusRevoked = "Revoked"
)

// certDefaultPageSize CertificateGetInstanceList 默认分页大小
const certDefaultPageSize = int32(50)

// ErrCertFiltered 证书库实例被过滤哨兵：revoked（IsCertificateRevoked=true）
// 或 status 非已签发（Issued）的实例不供导入（List/Get 两阶段过滤，任务 AC；
// 上层可用 errors.Is 识别为过滤态，与通用云 API 失败区分）。
var ErrCertFiltered = errors.New("volcano cert instance filtered (revoked or not issued)")

// certLibraryAPI 火山证书库 SDK 窄接口（只读两方法，*certificateservice.
// CERTIFICATESERVICE 天然满足）；WithContext 变体透传调用方取消/超时。
// 窄接口不含任何写方法——适配器的只读纪律由接口面构造性保证。
type certLibraryAPI interface {
	CertificateGetInstanceListWithContext(ctx context.Context, input *certificateservice.CertificateGetInstanceListInput, opts ...request.Option) (*certificateservice.CertificateGetInstanceListOutput, error)
	CertificateGetInstanceWithContext(ctx context.Context, input *certificateservice.CertificateGetInstanceInput, opts ...request.Option) (*certificateservice.CertificateGetInstanceOutput, error)
}

// CloudCertInstance 火山证书库在库实例（已过滤 revoked/非已签发，可供导入）：
// 要素字段与证书台账解析口径（cert/domain ParseCertAndKey 语义）一致，
// 台账聚合主键 fingerprint = SHA256(leaf.Raw) 小写 hex（^[0-9a-f]{64}$）。
// 不含任何私钥字段（Hard Rule：私钥只读后即丢，不入返回结构）。
type CloudCertInstance struct {
	CloudCertID  string    // 云侧实例 ID（InstanceId，发现导入的 cloudCertID）
	Fingerprint  string    // SHA256(leaf.Raw) 小写 hex（64 字符）
	CommonName   string    // leaf Subject CN
	San          []string  // leaf DNS SAN 列表（IP SAN 不计入域名口径）
	NotBefore    time.Time // 有效期起始（leaf）
	NotAfter     time.Time // 有效期截止（leaf）
	CertChainPEM string    // 仅 CERTIFICATE 块的净化序列（叶在前 fullchain 口径，云侧 Chain 序）
	Status       string    // 云侧实例状态（过滤后恒为 Issued，留存供诊断）
}

// CertAdapter 火山引擎证书库适配器（只读发现）。凭证复用既有云账号体系
// （*domain.CloudAccount，逐调用传入，不在适配层新建凭证存储）；
// SDK 客户端工厂字段可被测试注入 fake。
type CertAdapter struct {
	logger       *elog.Component
	listPageSize int32 // List 分页大小（默认 certDefaultPageSize，测试可缩小以覆盖翻页分支）

	newClient func(creds *domain.CloudAccount) (certLibraryAPI, error)
}

// NewCertAdapter 创建火山引擎证书库适配器（默认真实 SDK 客户端工厂；
// certificate_service 为全局服务，region 仅用于客户端签名装配）。
func NewCertAdapter(logger *elog.Component) *CertAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertAdapter{
		logger:       logger,
		listPageSize: certDefaultPageSize,
		newClient: func(creds *domain.CloudAccount) (certLibraryAPI, error) {
			config := volcengine.NewConfig().
				WithCredentials(credentials.NewStaticCredentials(creds.AccessKeyID, creds.AccessKeySecret, "")).
				WithRegion(certCredsRegion(creds))
			sess, err := session.NewSession(config)
			if err != nil {
				return nil, fmt.Errorf("创建证书服务会话失败: %w", err)
			}
			return certificateservice.New(sess), nil
		},
	}
}

// certPageSize 获取列举分页大小（零值回退默认）
func (a *CertAdapter) certPageSize() int32 {
	if a.listPageSize <= 0 {
		return certDefaultPageSize
	}
	return a.listPageSize
}

// ListCertificates 分页枚举证书库全部实例并逐实例解析证书材料（只读发现入口）：
//   - 分页语义（对齐 SDK 模型）：PageNumber 自 1 递增、PageSize 固定透传，
//     整页未满即最后一页；TotalCount 作防御性上界（防云侧短页语义漂移死循环）；
//   - 过滤（List 阶段）：IsCertificateRevoked=true 或 Status 非 Issued 的实例
//     直接跳过，不调 Get、不供导入（省云 API）；
//   - 单实例失败（Get 错误/链解析失败）：记入聚合错误并继续后续条目，
//     返回已获取部分 + 错误（不中断枚举）；List→Get 间隙状态变为过滤态的
//     实例按过滤处理，不算失败。
func (a *CertAdapter) ListCertificates(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertInstance, error) {
	if creds == nil {
		return nil, fmt.Errorf("volcano cert list: nil creds")
	}
	client, err := a.newClient(creds)
	if err != nil {
		return nil, err
	}
	var (
		items   []CloudCertInstance
		failed  []string
		fetched int32
	)
	for pageNumber := int32(1); ; pageNumber++ {
		out, err := a.listPage(ctx, client, pageNumber)
		if err != nil {
			return items, err
		}
		if len(out.Instances) == 0 {
			break
		}
		for _, inst := range out.Instances {
			item, ok := a.collectInstance(ctx, client, inst, &failed)
			if ok {
				items = append(items, item)
			}
		}
		fetched += int32(len(out.Instances))
		if int32(len(out.Instances)) < a.certPageSize() ||
			(out.TotalCount != nil && fetched >= *out.TotalCount) {
			break
		}
	}
	a.logger.Info("获取火山引擎证书库实例列表成功",
		elog.String("account", creds.Name),
		elog.Int("count", len(items)),
		elog.Int("failed", len(failed)))
	if len(failed) > 0 {
		return items, fmt.Errorf("volcano cert list: %d instance(s) failed: %s",
			len(failed), strings.Join(failed, "; "))
	}
	return items, nil
}

// GetCertificate 单实例证书材料（只读）：CertificateGetInstance → Chain（PEM 列表）
// → 台账要素解析（指纹 = SHA256(leaf.Raw) 小写 hex、CN = leaf Subject CN、
// SAN = leaf DNS SAN、有效期 = leaf NotBefore/NotAfter，与既有 CAS 口径一致；
// 指纹取链解析而非云侧 FingerPrintSha256 字段，保证与台账聚合主键同构）。
// revoked 或非已签发 status 的实例返回 ErrCertFiltered（Get 阶段过滤，不供导入）；
// 链缺失/无法解析 → 链解析失败错误（导入侧 checkChain 已支持系统信任库兜底，
// 适配器不自行补根、不做链完整性拦截）。
func (a *CertAdapter) GetCertificate(ctx context.Context, creds *domain.CloudAccount, instanceID string) (CloudCertInstance, error) {
	if creds == nil {
		return CloudCertInstance{}, fmt.Errorf("volcano cert get: nil creds")
	}
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return CloudCertInstance{}, fmt.Errorf("volcano cert get: empty instance id")
	}
	client, err := a.newClient(creds)
	if err != nil {
		return CloudCertInstance{}, err
	}
	return a.getCertificate(ctx, client, id)
}

// CloudCertKeyMaterial 证书材料（含私钥）：仅「导入即完整托管」路径使用
// （GetCertificateWithKey）。私钥为云侧返回的 PEM 原文（PKCS#1/RSA 等），
// 仅在本类型内流转；调用方须即时信封加密落库并 Zeroize 明文，不得进入
// 日志/错误信息/任何持久形态。
type CloudCertKeyMaterial struct {
	CloudCertID   string
	CertChainPEM  string
	PrivateKeyPEM string
}

// GetCertificateWithKey 单实例证书材料（含私钥，只读）：GetCertificate 的
// 带私钥变体——火山 certificateservice 的 CertificateGetInstance 响应携带
// PrivateKey（PEM 原文，实测标准 PKCS#1），使火山证书可「导入即完整托管」
// （修正 deployer 期「csv 私钥不可再导出」的过保守裁决）。revoked/非已签发
// 实例同 GetCertificate 返回 ErrCertFiltered。
func (a *CertAdapter) GetCertificateWithKey(ctx context.Context, creds *domain.CloudAccount, instanceID string) (CloudCertKeyMaterial, error) {
	if creds == nil {
		return CloudCertKeyMaterial{}, fmt.Errorf("volcano cert get-key: nil creds")
	}
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return CloudCertKeyMaterial{}, fmt.Errorf("volcano cert get-key: empty instance id")
	}
	client, err := a.newClient(creds)
	if err != nil {
		return CloudCertKeyMaterial{}, err
	}
	out, err := a.fetchCertificate(ctx, client, id)
	if err != nil {
		return CloudCertKeyMaterial{}, err
	}
	rawChain := concatCertChainPEM(out.CertificateDetail.Chain)
	chainPEM := cloudx.SanitizeCertChainPEM(rawChain)
	cloudx.Zeroize(rawChain)
	return CloudCertKeyMaterial{
		CloudCertID:   id,
		CertChainPEM:  chainPEM,
		PrivateKeyPEM: volcengine.StringValue(out.CertificateDetail.PrivateKey),
	}, nil
}

// listPage 单页实例列举（分页参数透传 + 错误归一）
func (a *CertAdapter) listPage(ctx context.Context, client certLibraryAPI, pageNumber int32) (*certificateservice.CertificateGetInstanceListOutput, error) {
	out, err := client.CertificateGetInstanceListWithContext(ctx, &certificateservice.CertificateGetInstanceListInput{
		PageNumber: volcengine.Int32(pageNumber),
		PageSize:   volcengine.Int32(a.certPageSize()),
	})
	if err != nil {
		return nil, wrapCertCloudErr("certificate_get_instance_list", err)
	}
	if out == nil {
		return nil, fmt.Errorf("volcano certificate_get_instance_list: empty response (page %d)", pageNumber)
	}
	return out, nil
}

// collectInstance 单实例采集：List 阶段过滤 → Get 链下载解析。
// 失败记入 failed 并继续（ok=false，不中断后续条目）；过滤实例静默跳过
// （Get 阶段过滤不算失败）。
func (a *CertAdapter) collectInstance(ctx context.Context, client certLibraryAPI, inst *certificateservice.InstanceForCertificateGetInstanceListOutput, failed *[]string) (CloudCertInstance, bool) {
	if inst == nil || inst.InstanceId == nil || *inst.InstanceId == "" {
		return CloudCertInstance{}, false
	}
	if certInstanceFiltered(volcengine.StringValue(inst.Status), volcengine.BoolValue(inst.IsCertificateRevoked)) {
		return CloudCertInstance{}, false
	}
	item, err := a.getCertificate(ctx, client, *inst.InstanceId)
	if err != nil {
		if errors.Is(err, ErrCertFiltered) {
			return CloudCertInstance{}, false
		}
		*failed = append(*failed, fmt.Sprintf("instance %s: %v", *inst.InstanceId, err))
		return CloudCertInstance{}, false
	}
	return item, true
}

// fetchCertificate 单实例详情拉取与形态校验（GetCertificate 与
// GetCertificateWithKey 共用内核）：GetInstance 调用 + 空响应/过滤/详情缺失
// 判定；通过后返回原始响应供调用方提取链（±私钥）。
func (a *CertAdapter) fetchCertificate(ctx context.Context, client certLibraryAPI, instanceID string) (*certificateservice.CertificateGetInstanceOutput, error) {
	out, err := client.CertificateGetInstanceWithContext(ctx, &certificateservice.CertificateGetInstanceInput{
		InstanceId: volcengine.String(instanceID),
	})
	if err != nil {
		return nil, wrapCertCloudErr("certificate_get_instance", err)
	}
	if out == nil {
		return nil, fmt.Errorf("volcano certificate_get_instance: empty response (instance %s)", instanceID)
	}
	status := volcengine.StringValue(out.Status)
	if certInstanceFiltered(status, volcengine.BoolValue(out.IsCertificateRevoked)) {
		return nil, fmt.Errorf("%w: instance %s (status=%s, revoked=%t)",
			ErrCertFiltered, instanceID, status, volcengine.BoolValue(out.IsCertificateRevoked))
	}
	if out.CertificateDetail == nil {
		return nil, fmt.Errorf("volcano certificate_get_instance: instance %s returned no certificate detail", instanceID)
	}
	return out, nil
}

// getCertificate 单实例详情与链解析（GetCertificate 与 List 枚举共用内核；
// 私钥卫生：本路径从不读取响应 PrivateKey 字段）
func (a *CertAdapter) getCertificate(ctx context.Context, client certLibraryAPI, instanceID string) (CloudCertInstance, error) {
	out, err := a.fetchCertificate(ctx, client, instanceID)
	if err != nil {
		return CloudCertInstance{}, err
	}
	status := volcengine.StringValue(out.Status)
	// 私钥卫生（Hard Rule）：证书材料仅取 Chain，经块级净化仅保留 CERTIFICATE
	// 块（PRIVATE KEY 等非证书内容构造性丢弃）；净化前的原始字节副本（可能
	// 携带非证书块）即刻归零。响应 PrivateKey 字段从不读取。
	rawChain := concatCertChainPEM(out.CertificateDetail.Chain)
	chainPEM := cloudx.SanitizeCertChainPEM(rawChain)
	cloudx.Zeroize(rawChain)
	return parseCertInstanceFromChain(instanceID, status, chainPEM)
}

// certInstanceFiltered 过滤判定：revoked 或 status 非已签发（Issued）→ 不供导入
func certInstanceFiltered(status string, revoked bool) bool {
	return revoked || status != certStatusIssued
}

// parseCertInstanceFromChain 链 PEM → 台账要素实例（CAS 口径）：
// 首个 CERTIFICATE 块视为 leaf，指纹 = SHA256(leaf.Raw) 小写 hex、
// CN = leaf Subject CN、SAN = leaf DNS SAN、有效期 = leaf NotBefore/NotAfter。
func parseCertInstanceFromChain(instanceID, status, chainPEM string) (CloudCertInstance, error) {
	leaf, ok := parseCertLeafPEM(chainPEM)
	if !ok {
		return CloudCertInstance{}, fmt.Errorf("volcano cert %s: chain parse failed: no valid CERTIFICATE PEM block", instanceID)
	}
	sum := sha256.Sum256(leaf.Raw)
	return CloudCertInstance{
		CloudCertID:  instanceID,
		Fingerprint:  hex.EncodeToString(sum[:]),
		CommonName:   leaf.Subject.CommonName,
		San:          append([]string(nil), leaf.DNSNames...),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
		CertChainPEM: chainPEM,
		Status:       status,
	}, nil
}

// parseCertLeafPEM 解析 PEM 证书束首个 CERTIFICATE 块为 leaf（无有效块 → false）
func parseCertLeafPEM(pemStr string) (*x509.Certificate, bool) {
	if pemStr == "" {
		return nil, false
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, false
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, false
	}
	return leaf, true
}

// concatCertChainPEM 拼接云侧 Chain（PEM 列表，叶在前口径由云侧返回顺序保证）：
// 块间补换行，避免块尾与下一块头粘连导致 pem.Decode 漏块；空/nil 元素跳过。
func concatCertChainPEM(chain []*string) []byte {
	var out []byte
	for _, item := range chain {
		if item == nil || strings.TrimSpace(*item) == "" {
			continue
		}
		out = append(out, []byte(strings.TrimSpace(*item))...)
		out = append(out, '\n')
	}
	return out
}

// wrapCertCloudErr 云 API 错误统一包装（对齐既有跨云 wrapCertCloudErr 归一语义）：
// 统一云/操作前缀 + %w 透传云侧错误明文（云侧细节明文允许；私钥材料不存在于
// 本适配器任何路径，详见文件头 Hard Rules）。
func wrapCertCloudErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("volcano %s api error: %w", op, err)
}

// certCredsRegion 账号默认地域（Regions[0]，缺省 cn-beijing，与既有火山适配器同口径）
func certCredsRegion(creds *domain.CloudAccount) string {
	if creds == nil || len(creds.Regions) == 0 {
		return "cn-beijing"
	}
	return creds.Regions[0]
}
