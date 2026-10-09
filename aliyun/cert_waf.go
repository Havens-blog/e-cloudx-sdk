package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/gotomicro/ego/core/elog"
)

// WAF 3.0 证书产品实现。
// SDK waf_openapi 包未覆盖域名/证书域 OpenAPI，按公开文档（wafopenapi 2021-10-01）直调，
// 与既有 waf.go 的 CommonRequest 约定一致（PoC 任务 5.12 联动验证）。
// 发现：DescribeDefenseResources（分页）→ 逐域名 DescribeDomainDetail 提取 Listen.CertId；
// 绑定：读 DescribeDomainDetail 快照 → ModifyDomain 替换 Listen.CertId（Redirect 原样回写，避免配置丢失）。

// wafCertVersion WAF 3.0 OpenAPI 版本
const wafCertVersion = "2021-10-01"

// wafCertCaller WAF 3.0 RPC 调用抽象（真实实现走 ProcessCommonRequest，测试注入 fake）
type wafCertCaller interface {
	call(action string, params map[string]string) (json.RawMessage, error)
}

// wafRPCInvoker WAF 3.0 RPC 真实调用器（复用 waf.go 的 CommonRequest 风格）
type wafRPCInvoker struct {
	client *sdk.Client
	region string
}

func (w *wafRPCInvoker) call(action string, params map[string]string) (json.RawMessage, error) {
	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = wafCertEndpoint(w.region)
	request.Version = wafCertVersion
	request.ApiName = action
	request.QueryParams["RegionId"] = w.region
	for key, value := range params {
		request.QueryParams[key] = value
	}
	response, err := w.client.ProcessCommonRequest(request)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(response.GetHttpContentBytes()), nil
}

// newWafSDKClient 构建 WAF 3.0 基础 SDK 客户端
func newWafSDKClient(region, accessKeyID, accessKeySecret string) (*sdk.Client, error) {
	return sdk.NewClientWithAccessKey(region, accessKeyID, accessKeySecret)
}

// wafCertEndpoint WAF 3.0 API 端点（中国站 cn-hangzhou，国际站 ap-southeast-1）
func wafCertEndpoint(region string) string {
	if region == "ap-southeast-1" {
		return "wafopenapi.ap-southeast-1.aliyuncs.com"
	}
	return "wafopenapi.cn-hangzhou.aliyuncs.com"
}

// wafRegionOf WAF 3.0 服务地域归一化（全局服务，仅两地域）
func wafRegionOf(region string) string {
	if region == "ap-southeast-1" {
		return region
	}
	return "cn-hangzhou"
}

// wafCertDomainDetail DescribeDomainDetail 响应子集（Listen/Redirect 原文透传）
type wafCertDomainDetail struct {
	Listen   json.RawMessage `json:"Listen"`
	Redirect json.RawMessage `json:"Redirect"`
}

// wafCertListenInfo Listen 对象的证书字段子集
type wafCertListenInfo struct {
	CertID string `json:"CertId"`
}

// listWAFReferences 分页遍历 WAF 防护对象，逐域名提取 HTTPS 监听证书 ID
func (a *CertAdapter) listWAFReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aliyun waf cert list: nil creds")
	}
	region := wafRegionOf(credsRegion(creds))
	caller, err := a.newWafCaller(creds, region)
	if err != nil {
		return nil, err
	}

	instanceID, err := a.wafInstanceID(ctx, caller)
	if err != nil {
		return nil, err
	}

	accountKey := creds.Name
	var refs []CloudCertRef
	pageNumber := 1
	collected := 0
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		body, err := caller.call("DescribeDefenseResources", map[string]string{
			"PageNumber": strconv.Itoa(pageNumber),
			"PageSize":   strconv.Itoa(a.certPageSize()),
		})
		if err != nil {
			return nil, wrapCertCloudErr(CertProductWAF, err)
		}
		var page describeDefenseResourcesResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("aliyun waf parse defense resources failed: %w", err)
		}
		collected += len(page.Resources)
		for _, res := range page.Resources {
			// Detail 中的 domain 才是真实域名，Resource 可能带 -waf 后缀（复用 waf.go 逻辑）
			domain := extractDomainFromDetail(res.Detail)
			if domain == "" {
				domain = res.Resource
			}
			certID, err := a.wafDomainCertID(ctx, caller, instanceID, domain)
			if err != nil {
				// 单域名详情失败跳过继续（与 waf.go 详情增强降级口径一致）
				a.logger.Warn("获取WAF域名证书信息失败，跳过",
					elog.String("domain", domain),
					elog.FieldErr(err))
				continue
			}
			if certID == "" {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "aliyun",
				Product:               CertProductWAF,
				ResourceID:            domain,
				ReferencedCloudCertID: certID,
				AccountKey:            accountKey,
			})
		}
		if len(page.Resources) < a.certPageSize() {
			break
		}
		if page.TotalCount > 0 && collected >= int(page.TotalCount) {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云WAF证书引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}

// wafDomainCertID 查询单个 WAF 防护域名的 HTTPS 监听证书 ID（未配置证书返回空串）
func (a *CertAdapter) wafDomainCertID(ctx context.Context, caller wafCertCaller, instanceID, domain string) (string, error) {
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}
	body, err := caller.call("DescribeDomainDetail", map[string]string{
		"InstanceId": instanceID,
		"Domain":     domain,
	})
	if err != nil {
		return "", err
	}
	var detail wafCertDomainDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return "", fmt.Errorf("aliyun waf parse domain detail failed: %w", err)
	}
	if len(detail.Listen) == 0 {
		return "", nil
	}
	var listen wafCertListenInfo
	if err := json.Unmarshal(detail.Listen, &listen); err != nil {
		return "", fmt.Errorf("aliyun waf parse listen config failed: %w", err)
	}
	return listen.CertID, nil
}

// bindWAF 读改写绑定：读取域名当前监听/回源配置 → 替换 Listen.CertId → ModifyDomain 全量回写。
// Listen 以 map 形态整体修改后回写（保留未知字段）；Redirect 原样透传，避免非证书配置被清空。
func (a *CertAdapter) bindWAF(ctx context.Context, creds *domain.CloudAccount, domain, cloudCertID string) error {
	if creds == nil {
		return fmt.Errorf("aliyun waf cert bind: nil creds")
	}
	if domain == "" {
		return fmt.Errorf("aliyun waf cert bind: empty domain")
	}
	region := wafRegionOf(credsRegion(creds))
	caller, err := a.newWafCaller(creds, region)
	if err != nil {
		return err
	}

	instanceID, err := a.wafInstanceID(ctx, caller)
	if err != nil {
		return err
	}

	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	body, err := caller.call("DescribeDomainDetail", map[string]string{
		"InstanceId": instanceID,
		"Domain":     domain,
	})
	if err != nil {
		return wrapCertCloudErr(CertProductWAF, err)
	}
	var detail wafCertDomainDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return fmt.Errorf("aliyun waf parse domain detail failed: %w", err)
	}
	if len(detail.Listen) == 0 || string(detail.Listen) == "null" {
		return fmt.Errorf("aliyun waf cert bind: domain %s has no Listen config", domain)
	}
	var listen map[string]any
	if err := json.Unmarshal(detail.Listen, &listen); err != nil {
		return fmt.Errorf("aliyun waf parse listen config failed: %w", err)
	}
	// 无 HTTPS 监听端口的域名绑定证书无意义，显式报错而非下发残缺配置
	httpsPorts, _ := listen["HttpsPorts"].([]any)
	if len(httpsPorts) == 0 {
		return fmt.Errorf("aliyun waf cert bind: domain %s has no HTTPS ports in Listen config", domain)
	}
	listen["CertId"] = cloudCertID
	listenBytes, err := json.Marshal(listen)
	if err != nil {
		return fmt.Errorf("aliyun waf marshal listen config failed: %w", err)
	}

	params := map[string]string{
		"InstanceId": instanceID,
		"Domain":     domain,
		"Listen":     string(listenBytes),
	}
	if len(detail.Redirect) > 0 && string(detail.Redirect) != "null" {
		params["Redirect"] = string(detail.Redirect)
	}

	if err := a.waitRateLimit(ctx); err != nil {
		return err
	}
	if _, err := caller.call("ModifyDomain", params); err != nil {
		return wrapCertCloudErr(CertProductWAF, err)
	}
	a.logger.Info("阿里云WAF证书绑定成功",
		elog.String("domain", domain),
		elog.String("cloud_cert_id", cloudCertID))
	return nil
}

// wafInstanceID 查询 WAF 实例 ID（证书域名操作前置条件）
func (a *CertAdapter) wafInstanceID(ctx context.Context, caller wafCertCaller) (string, error) {
	if err := a.waitRateLimit(ctx); err != nil {
		return "", err
	}
	body, err := caller.call("DescribeInstance", nil)
	if err != nil {
		return "", wrapCertCloudErr(CertProductWAF, err)
	}
	var resp describeInstanceResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("aliyun waf parse instance failed: %w", err)
	}
	if resp.InstanceID == "" {
		return "", fmt.Errorf("aliyun waf: empty instance id")
	}
	return resp.InstanceID, nil
}
