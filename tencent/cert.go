package tencent

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	commonhttp "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/http"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"
)

// 证书域公共类型与入口（tech-design Interface 2: CloudDeployer 的腾讯云 SDK 适配层）。
// 本层仅做单次云 API 调用封装：两段式第一段（UploadCert）/第二段（BindResource）、
// 只读发现（ListReferences）、回滚目标校验（GetCert）、孤儿清理（CleanupOrphan）；
// 不做业务编排（上传+绑定编排、批次、验证窗口等属 cert 功能域 service/deployer 层）。
// 方法签名与 3.1 阿里云适配同构（creds + product 参数化），供 5.3 统一抽象消费。
//
// 腾讯云 SSL 证书服务（ssl 2019-12-05）产品模块未纳入仓库依赖，按 3.1 WAF 3.0 的
// SDK 覆盖缺口处理口径：经 common SDK 的 CommonRequest 直调公开 OpenAPI
// （与 aliyun/cert_waf.go 的 wafRPCInvoker 同风格，PoC 任务 5.12 联动验证）。

// 证书产品枚举（对齐 schema.sql cert_references.product 首期腾讯云可部署产品集：
// tencent: cdn/waf(EdgeOne)/clb）
const (
	// CertProductCDN CDN 加速域名
	CertProductCDN = "cdn"
	// CertProductWAF 腾讯云侧映射 EdgeOne 站点接入域名（waf(EdgeOne)，
	// 见任务 3.2 参考文件 schema.sql product 枚举注释）
	CertProductWAF = "waf"
	// CertProductCLB 负载均衡 CLB 监听器
	CertProductCLB = "clb"
)

// certSupportedProducts 首期支持的证书可部署产品（dcdn/alb/nlb 等未实现产品显式报错）
var certSupportedProducts = map[string]bool{
	CertProductCDN: true,
	CertProductWAF: true,
	CertProductCLB: true,
}

// 证书域哨兵错误
var (
	// ErrCloudRateLimited 云 API 限流/流控（CLOUD_API_RATELIMITED 语义）。
	// 定义位于 cloudx 公共层（tech-design Error Handling），本包再导出以对齐 3.1 调用形态；
	// 限流重试与退避策略属变更执行编排层，本层仅映射哨兵。
	ErrCloudRateLimited = cloudx.ErrCloudRateLimited
	// ErrCertProductNotSupported 未实现的证书产品/接口（显式报错而非静默）
	ErrCertProductNotSupported = errors.New("tencent cert product not supported")
)

// CloudCertRef 云侧证书引用（对齐 CertReference 落库字段需求：
// cloud/product/resourceId/referencedCloudCertId/accountKey，见 schema.sql cert_references）
type CloudCertRef struct {
	Cloud                 string // 固定 "tencent"
	Product               string // cdn|waf(EdgeOne)|clb
	ResourceID            string // 云资源标识：CDN=域名；EdgeOne="{ZoneId}/{Host}"；CLB="{LoadBalancerId}/{ListenerId}"
	ReferencedCloudCertID string // 云侧证书 ID（腾讯云 SSL 证书库 CertificateId）
	AccountKey            string // 云账号标识（取 CloudAccount.Name）
}

// CloudCertInfo GetCert 返回的云侧证书在库状态（回滚目标有效性校验依据）
type CloudCertInfo struct {
	Exists      bool      // 云证书库中该 cloudCertId 是否存在
	NotAfter    time.Time // 云侧证书有效期截止；Exists=false 时零值
	Fingerprint string    // 优先 PEM 解析的 SHA256 hex（对齐台账指纹 ^[0-9a-f]{64}$）；无 PEM 时回退云侧指纹字段
	// CertChainPEM 仅 CERTIFICATE 块的净化序列（叶在前 fullchain 口径）：
	// 块级过滤构造性保证（cloudx.SanitizeCertChainPEM），不含 PRIVATE KEY 等
	// 任何非证书内容；云侧未返回 PEM 时为空。发现导入材料通道（cert-cloud-discovery-import）。
	CertChainPEM string
}

// SSL 证书服务直调常量（ssl 为全局服务，region 不参与寻址）
const (
	sslService  = "ssl"
	sslVersion  = "2019-12-05"
	sslEndpoint = "ssl.tencentcloudapi.com"
)

// sslCertCaller SSL 证书服务 RPC 调用抽象（真实实现走 CommonRequest，测试注入 fake）
type sslCertCaller interface {
	call(action string, params map[string]interface{}) (json.RawMessage, error)
}

// sslRPCInvoker SSL 证书服务真实调用器（复用仓库既有 tencent common SDK 依赖）
type sslRPCInvoker struct {
	client *common.Client
}

// newSSLRPCInvoker 构建 SSL 证书服务调用器（SDK 客户端构建不发起网络请求）
func newSSLRPCInvoker(creds *domain.CloudAccount) (*sslRPCInvoker, error) {
	credential := common.NewCredential(creds.AccessKeyID, creds.AccessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = sslEndpoint
	return &sslRPCInvoker{client: common.NewCommonClient(credential, "", cpf)}, nil
}

// call 发起 SSL 服务 RPC 调用，返回响应体原文（含 Response 信封）
func (s *sslRPCInvoker) call(action string, params map[string]interface{}) (json.RawMessage, error) {
	request := commonhttp.NewCommonRequest(sslService, sslVersion, action)
	if err := request.SetActionParameters(params); err != nil {
		return nil, fmt.Errorf("构建SSL请求参数失败: %w", err)
	}
	response := commonhttp.NewCommonResponse()
	if err := s.client.Send(request, response); err != nil {
		return nil, err
	}
	return json.RawMessage(response.GetBody()), nil
}

// cdnCertAPI CDN SDK 窄接口（cert_cdn.go 使用）
type cdnCertAPI interface {
	DescribeDomainsConfig(request *tencentcdn.DescribeDomainsConfigRequest) (*tencentcdn.DescribeDomainsConfigResponse, error)
	UpdateDomainConfig(request *tencentcdn.UpdateDomainConfigRequest) (*tencentcdn.UpdateDomainConfigResponse, error)
}

// teoCertAPI EdgeOne SDK 窄接口（cert_edgeone.go 使用）
type teoCertAPI interface {
	DescribeZones(request *teo.DescribeZonesRequest) (*teo.DescribeZonesResponse, error)
	DescribeHostsSetting(request *teo.DescribeHostsSettingRequest) (*teo.DescribeHostsSettingResponse, error)
	ModifyHostsCertificate(request *teo.ModifyHostsCertificateRequest) (*teo.ModifyHostsCertificateResponse, error)
}

// clbCertAPI CLB SDK 窄接口（cert_clb.go 使用）
type clbCertAPI interface {
	DescribeLoadBalancers(request *clb.DescribeLoadBalancersRequest) (*clb.DescribeLoadBalancersResponse, error)
	DescribeListeners(request *clb.DescribeListenersRequest) (*clb.DescribeListenersResponse, error)
	ModifyListener(request *clb.ModifyListenerRequest) (*clb.ModifyListenerResponse, error)
}

// CertAdapter 腾讯云证书适配器：按产品分发五方法。
// 凭证复用既有云账号体系（*domain.CloudAccount），逐调用传入，
// 不在适配层新建凭证存储；SDK 客户端工厂字段可被测试注入 fake。
type CertAdapter struct {
	logger       *elog.Component
	rateLimiter  *ratelimit.RateLimiter
	listPageSize int // ListReferences 分页大小（默认 certDefaultPageSize，测试可缩小以覆盖翻页分支）

	deletePollInterval time.Duration // 孤儿删除异步任务轮询间隔（默认 certDeletePollInterval，测试可注入）
	deletePollAttempts int           // 孤儿删除异步任务轮询次数上限（默认 certDeletePollAttempts，测试可缩小）

	newSSLCaller func(creds *domain.CloudAccount) (sslCertCaller, error)
	newCdnClient func(creds *domain.CloudAccount) (cdnCertAPI, error)
	newTeoClient func(creds *domain.CloudAccount) (teoCertAPI, error)
	newClbClient func(creds *domain.CloudAccount, region string) (clbCertAPI, error)
}

// certDefaultPageSize ListReferences 默认分页大小
const certDefaultPageSize = 50

// NewCertAdapter 创建腾讯云证书适配器（默认真实 SDK 客户端工厂，复用既有账号凭证体系）
func NewCertAdapter(logger *elog.Component) *CertAdapter {
	if logger == nil {
		logger = elog.DefaultLogger
	}
	return &CertAdapter{
		logger:       logger,
		rateLimiter:  ratelimit.NewRateLimiter(20),
		listPageSize: certDefaultPageSize,
		newSSLCaller: func(creds *domain.CloudAccount) (sslCertCaller, error) {
			return newSSLRPCInvoker(creds)
		},
		newCdnClient: func(creds *domain.CloudAccount) (cdnCertAPI, error) {
			return newCertCDNClient(creds)
		},
		newTeoClient: func(creds *domain.CloudAccount) (teoCertAPI, error) {
			return newCertTEOClient(creds)
		},
		newClbClient: func(creds *domain.CloudAccount, region string) (clbCertAPI, error) {
			return newCertCLBClient(creds, region)
		},
	}
}

// waitRateLimit 等待限流令牌
func (a *CertAdapter) waitRateLimit(ctx context.Context) error {
	if a.rateLimiter == nil {
		return nil
	}
	return a.rateLimiter.Wait(ctx)
}

// certPageSize 获取列举分页大小（零值回退默认）
func (a *CertAdapter) certPageSize() int {
	if a.listPageSize <= 0 {
		return certDefaultPageSize
	}
	return a.listPageSize
}

// UploadCert 两段式第一段：上传证书到腾讯云 SSL 证书库，返回 cloudCertId。
// 三产品统一经 ssl 服务上传（CertificateId 即各产品绑定引用的云证书 ID）。
// name 为云侧证书备注名（Alias）；certPEM/keyPEM 为 PEM 内容（仅透传云 API，不落日志）。
func (a *CertAdapter) UploadCert(ctx context.Context, creds *domain.CloudAccount, product, name, certPEM, keyPEM string) (string, error) {
	if err := checkCertProduct(product); err != nil {
		return "", err
	}
	if creds == nil {
		return "", fmt.Errorf("tencent cert upload: nil creds")
	}
	if name == "" || certPEM == "" || keyPEM == "" {
		return "", fmt.Errorf("tencent cert upload: name/cert/key required")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}

	caller, err := a.newSSLCaller(creds)
	if err != nil {
		return "", err
	}
	body, err := caller.call("UploadCertificate", map[string]interface{}{
		"CertificatePublicKey":  certPEM,
		"CertificatePrivateKey": keyPEM,
		"Alias":                 name,
		// 每次更换生成独立云证书副本：孤儿清理按 CloudCertMapping 归属收敛，
		// 避免复用可能仍被其他资源引用的存量证书（Repeatable=false 时云侧返回重复证书 ID）
		"Repeatable": true,
	})
	if err != nil {
		return "", wrapCertCloudErr(product, err)
	}

	var resp struct {
		Response struct {
			CertificateId string `json:"CertificateId"`
			RepeatCertId  string `json:"RepeatCertId"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("tencent %s upload cert: parse response failed: %w", product, err)
	}
	cloudCertID := resp.Response.CertificateId
	if cloudCertID == "" {
		cloudCertID = resp.Response.RepeatCertId
	}
	if cloudCertID == "" {
		return "", fmt.Errorf("tencent %s upload cert: empty cloud cert id", product)
	}
	a.logger.Info("腾讯云证书上传成功",
		elog.String("product", product),
		elog.String("cloud_cert_id", cloudCertID))
	return cloudCertID, nil
}

// BindResource 两段式第二段：将 cloudCertId 绑定到产品资源（按产品分发，详见各 cert_*.go）
func (a *CertAdapter) BindResource(ctx context.Context, creds *domain.CloudAccount, product, resourceID, cloudCertID string) error {
	switch product {
	case CertProductCDN:
		return a.bindCDN(ctx, creds, resourceID, cloudCertID)
	case CertProductWAF:
		return a.bindEdgeOne(ctx, creds, resourceID, cloudCertID)
	case CertProductCLB:
		return a.bindCLB(ctx, creds, resourceID, cloudCertID)
	default:
		return certProductNotSupported(product)
	}
}

// ListReferences 只读发现：列出产品下全部证书引用（按产品分发，详见各 cert_*.go）
func (a *CertAdapter) ListReferences(ctx context.Context, creds *domain.CloudAccount, product string) ([]CloudCertRef, error) {
	switch product {
	case CertProductCDN:
		return a.listCDNReferences(ctx, creds)
	case CertProductWAF:
		return a.listEdgeOneReferences(ctx, creds)
	case CertProductCLB:
		return a.listCLBReferences(ctx, creds)
	default:
		return nil, certProductNotSupported(product)
	}
}

// GetCert 查询云侧证书在库状态（回滚目标有效性校验依据，只读）。
// 产品无关：统一走 ssl DescribeCertificateDetail。
func (a *CertAdapter) GetCert(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) (CloudCertInfo, error) {
	if creds == nil {
		return CloudCertInfo{}, fmt.Errorf("tencent cert get: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return CloudCertInfo{}, fmt.Errorf("tencent cert get: empty cloud cert id")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return CloudCertInfo{}, err
	}

	caller, err := a.newSSLCaller(creds)
	if err != nil {
		return CloudCertInfo{}, err
	}
	body, err := caller.call("DescribeCertificateDetail", map[string]interface{}{
		"CertificateId": cloudCertID,
	})
	if err != nil {
		if isCertNotFoundError(err) {
			// 云侧已删除 → Exists=false（非错误，回滚目标校验按无效处理）
			return CloudCertInfo{Exists: false}, nil
		}
		return CloudCertInfo{}, wrapCertCloudErr("ssl", err)
	}

	var resp struct {
		Response struct {
			CertificatePublicKey string `json:"CertificatePublicKey"`
			CertEndTime          string `json:"CertEndTime"`
			CertFingerprint      string `json:"CertFingerprint"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return CloudCertInfo{}, fmt.Errorf("tencent cert get: parse response failed: %w", err)
	}

	info := CloudCertInfo{Exists: true}
	// PEM 通道净化（构造性保证）：仅保留 CERTIFICATE 块的净化序列，
	// 原始字节副本净化后即刻归零（私钥等非证书内容不驻留）。
	rawCert := []byte(resp.Response.CertificatePublicKey)
	info.CertChainPEM = cloudx.SanitizeCertChainPEM(rawCert)
	cloudx.Zeroize(rawCert)
	if leaf, ok := parseCertLeafPEM(info.CertChainPEM); ok {
		sum := sha256.Sum256(leaf.Raw)
		info.Fingerprint = hex.EncodeToString(sum[:])
		info.NotAfter = leaf.NotAfter
	} else {
		// 云侧未返回 PEM 内容时回退指纹字段：注意腾讯云 CertFingerprint 为 SHA1 形态，
		// 与台账 SHA256 指纹口径不一致（空值/形态差异由上层按"无法复核"处理，PoC 5.12 登记验证）
		info.Fingerprint = normalizeCloudFingerprint(resp.Response.CertFingerprint)
		if notAfter, ok := parseCloudCertTime(resp.Response.CertEndTime); ok {
			info.NotAfter = notAfter
		}
	}
	return info, nil
}

// CleanupOrphan 孤儿清理：删除 SSL 证书库中不再被引用的云证书（幂等：已不存在视为成功）。
//
// B1 修正（poc-notes §5-B1/§6-C1 方案 B）：IsCheckResource=true 时 DeleteCertificate
// 为异步任务——响应 {DeleteResult:false, TaskId}，需轮询 DescribeDeleteCertificatesTaskResult
// 确认结果，2xx ≠ 已删除。本方法内部按响应分派：
//   - DeleteResult=true → 同步删除完成；
//   - DeleteResult=false 且 TaskId 非空 → 有界轮询任务结果（预算耗尽返回错误，
//     清理队列重放安全：已删除证书再删返回不存在 → 幂等成功）；
//   - 两者皆无 → 防御性失败（无法确认删除，不得记"清理成功"）。
func (a *CertAdapter) CleanupOrphan(ctx context.Context, creds *domain.CloudAccount, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("tencent cert cleanup: nil creds")
	}
	if strings.TrimSpace(cloudCertID) == "" {
		return fmt.Errorf("tencent cert cleanup: empty cloud cert id")
	}
	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}

	caller, err := a.newSSLCaller(creds)
	if err != nil {
		return err
	}
	// IsCheckResource=true：云侧二次校验证书仍被云资源引用时拒绝删除（孤儿语义兜底）
	body, err := caller.call("DeleteCertificate", map[string]interface{}{
		"CertificateId":   cloudCertID,
		"IsCheckResource": true,
	})
	if err != nil {
		if isCertNotFoundError(err) {
			// 已被删除 → 幂等成功（清理队列重放场景）
			return nil
		}
		return wrapCertCloudErr("ssl", err)
	}
	var resp struct {
		Response struct {
			DeleteResult bool   `json:"DeleteResult"`
			TaskId       string `json:"TaskId"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("tencent ssl delete cert: parse response failed: %w", err)
	}
	if resp.Response.DeleteResult {
		a.logger.Info("腾讯云孤儿证书清理成功（同步删除）",
			elog.String("cloud_cert_id", cloudCertID))
		return nil
	}
	if resp.Response.TaskId == "" {
		return fmt.Errorf("tencent ssl delete cert %s: no delete confirmation (DeleteResult=false, empty TaskId)", cloudCertID)
	}
	return a.waitDeleteCertTask(ctx, caller, cloudCertID, resp.Response.TaskId)
}

// 孤儿删除异步任务轮询参数（有界：禁无限轮询）
const (
	// certDeletePollInterval 轮询间隔
	certDeletePollInterval = 2 * time.Second
	// certDeletePollAttempts 轮询次数上限（最坏阻塞 2s×10=20s）
	certDeletePollAttempts = 10
)

// DeleteTaskResult.Status 任务状态（官方文档三态：删除中/删除成功/删除失败；
// 数值映射待实网复核 poc-notes §8-P3，Error 字段非空按失败双信号兜底）。
const (
	deleteTaskStatusSuccess int64 = 1
	deleteTaskStatusFailed  int64 = 2
)

// certDeletePollIntervalOf 轮询间隔（零值回退默认）。
func (a *CertAdapter) certDeletePollIntervalOf() time.Duration {
	if a.deletePollInterval <= 0 {
		return certDeletePollInterval
	}
	return a.deletePollInterval
}

// certDeletePollAttemptsOf 轮询次数上限（零值回退默认）。
func (a *CertAdapter) certDeletePollAttemptsOf() int {
	if a.deletePollAttempts <= 0 {
		return certDeletePollAttempts
	}
	return a.deletePollAttempts
}

// waitDeleteCertTask 有界轮询异步删除任务结果：
//   - 删除成功 → nil；删除失败 / Error 非空 → 携带云侧详情的错误；
//   - 删除中 / 任务未出现在结果列表 → 继续轮询；
//   - 单次查询失败（限流等）不中断，占用一次轮询预算继续；
//   - 预算耗尽仍无终态 → 错误返回（清理队列重放兜底）。
func (a *CertAdapter) waitDeleteCertTask(ctx context.Context, caller sslCertCaller, cloudCertID, taskID string) error {
	interval := a.certDeletePollIntervalOf()
	attempts := a.certDeletePollAttemptsOf()
	var lastQueryErr error
	for i := 0; i < attempts; i++ {
		if err := sleepCtx(ctx, interval); err != nil {
			return fmt.Errorf("tencent ssl delete task %s poll interrupted: %w", taskID, err)
		}
		status, taskErr, err := a.queryDeleteCertTask(ctx, caller, taskID)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("tencent ssl delete task %s poll canceled: %w", taskID, ctx.Err())
			}
			lastQueryErr = err
			continue
		}
		if taskErr != "" || status == deleteTaskStatusFailed {
			return fmt.Errorf("tencent ssl delete task %s 删除失败 (status=%d): %s", taskID, status, taskErr)
		}
		if status == deleteTaskStatusSuccess {
			a.logger.Info("腾讯云孤儿证书清理成功（异步任务完成）",
				elog.String("cloud_cert_id", cloudCertID),
				elog.String("task_id", taskID))
			return nil
		}
		// 删除中 / 未知状态（数值映射未实网复核）→ 继续轮询，不静默视为成功
	}
	return fmt.Errorf("tencent ssl delete task %s unfinished after %d polls (interval %s): %w",
		taskID, attempts, interval, lastQueryErr)
}

// queryDeleteCertTask 查询单个删除任务结果，返回 (status, error详情, 查询错误)；
// 任务未出现在结果列表时 status=-1（视为进行中，由调用方继续轮询）。
func (a *CertAdapter) queryDeleteCertTask(ctx context.Context, caller sslCertCaller, taskID string) (int64, string, error) {
	if err := a.waitRateLimit(ctx); err != nil {
		return 0, "", err
	}
	body, err := caller.call("DescribeDeleteCertificatesTaskResult", map[string]interface{}{
		"TaskIds": []string{taskID},
	})
	if err != nil {
		return 0, "", err
	}
	var resp struct {
		Response struct {
			DeleteTaskResult []struct {
				TaskId string `json:"TaskId"`
				Status *int64 `json:"Status"`
				Error  string `json:"Error"`
			} `json:"DeleteTaskResult"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, "", fmt.Errorf("parse task result failed: %w", err)
	}
	for _, r := range resp.Response.DeleteTaskResult {
		if r.TaskId != taskID {
			continue
		}
		status := int64(-1)
		if r.Status != nil {
			status = *r.Status
		}
		return status, r.Error, nil
	}
	return -1, "", nil
}

// sleepCtx 可被 ctx 取消打断的等待。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ==================== 公共辅助 ====================

// checkCertProduct 校验产品是否在首期支持集合
func checkCertProduct(product string) error {
	if certSupportedProducts[product] {
		return nil
	}
	return certProductNotSupported(product)
}

func certProductNotSupported(product string) error {
	return fmt.Errorf("%w: %q", ErrCertProductNotSupported, product)
}

// wrapCertCloudErr 云 API 错误统一包装：限流/流控映射哨兵 ErrCloudRateLimited，其余带产品上下文透传
func wrapCertCloudErr(product string, err error) error {
	if err == nil {
		return nil
	}
	if isCertRateLimited(err) {
		return fmt.Errorf("%w: tencent %s api throttled: %v", ErrCloudRateLimited, product, err)
	}
	return fmt.Errorf("tencent %s api error: %w", product, err)
}

// isCertRateLimited 判定腾讯云限流/流控错误（RequestLimitExceeded / LimitExceeded 族错误码）
func isCertRateLimited(err error) bool {
	if err == nil {
		return false
	}
	var sdkErr *tcerr.TencentCloudSDKError
	if errors.As(err, &sdkErr) {
		return isRateLimitCode(sdkErr.Code)
	}
	text := err.Error()
	return strings.Contains(text, "RequestLimitExceeded") || strings.Contains(text, "LimitExceeded")
}

func isRateLimitCode(code string) bool {
	return code == "LimitExceeded" || strings.HasPrefix(code, "LimitExceeded.") ||
		code == "RequestLimitExceeded" || strings.HasPrefix(code, "RequestLimitExceeded.")
}

// isCertNotFoundError 判定证书不存在错误（云侧错误码或文案含非存在语义）
func isCertNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var sdkErr *tcerr.TencentCloudSDKError
	if errors.As(err, &sdkErr) {
		return isNotFoundText(sdkErr.Code) || isNotFoundText(sdkErr.Message)
	}
	return isNotFoundText(err.Error())
}

// isNotFoundText 错误码/文案的非存在语义判定（大小写不敏感子串，含中文文案）
func isNotFoundText(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "notfound") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "not exist") ||
		strings.Contains(lower, "notexist") ||
		strings.Contains(s, "不存在")
}

// parseCertScopedResourceID 解析 "{owner}/{sub}" 复合资源 ID（EdgeOne=ZoneId/Host，CLB=LoadBalancerId/ListenerId）
func parseCertScopedResourceID(resourceID string) (string, string, error) {
	parts := strings.SplitN(resourceID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("tencent cert: invalid scoped resource id %q", resourceID)
	}
	return parts[0], parts[1], nil
}

// parseCertLeafPEM 解析 PEM 证书内容（GetCert 优先按 PEM 计算 SHA256 指纹）
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

// normalizeCloudFingerprint 云侧冒号分隔指纹归一化为小写无分隔 hex（尽力对齐台账指纹形态）
func normalizeCloudFingerprint(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	lower := strings.ToLower(fingerprint)
	if !strings.Contains(lower, ":") {
		return strings.TrimSpace(lower)
	}
	parts := strings.Split(lower, ":")
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(strings.TrimSpace(part))
	}
	return b.String()
}

// certTimezoneGMT8 腾讯云时间字段口径时区（GMT+8）
var certTimezoneGMT8 = time.FixedZone("GMT+8", 8*3600)

// parseCloudCertTime 解析云侧时间字段（腾讯云文档口径 GMT+8；兼容 RFC3339 带时区布局）
func parseCloudCertTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, certTimezoneGMT8); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// certCredsRegion 账号默认地域（Regions[0]，缺省 ap-guangzhou，与 Adapter 同口径）
func certCredsRegion(creds *domain.CloudAccount) string {
	if creds == nil || len(creds.Regions) == 0 {
		return "ap-guangzhou"
	}
	return creds.Regions[0]
}

// certCredsRegions 账号地域清单（CLB 按地域遍历发现；缺省回退默认地域）
func certCredsRegions(creds *domain.CloudAccount) []string {
	if creds == nil || len(creds.Regions) == 0 {
		return []string{certCredsRegion(creds)}
	}
	return creds.Regions
}
