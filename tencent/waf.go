package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"
	waf "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/waf/v20180125"
)

// WAFAdapter 腾讯云WAF适配器
// 同步 WAF 防护域名 + EdgeOne(EO) 站点，统一归类为 WAF 防护资源
type WAFAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewWAFAdapter 创建WAF适配器
func NewWAFAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *WAFAdapter {
	return &WAFAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createWAFClient 创建WAF客户端
func (a *WAFAdapter) createWAFClient(region string) (*waf.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "waf.tencentcloudapi.com"
	return waf.NewClient(credential, region, cpf)
}

// createTEOClient 创建 EdgeOne 客户端
func (a *WAFAdapter) createTEOClient() (*teo.Client, error) {
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "teo.tencentcloudapi.com"
	// TEO 是全局服务，region 不影响
	return teo.NewClient(credential, "", cpf)
}

// ListInstances 获取WAF防护域名 + EO站点列表
func (a *WAFAdapter) ListInstances(ctx context.Context, region string) ([]types.WAFInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个WAF防护域名详情
func (a *WAFAdapter) GetInstance(ctx context.Context, region, instanceID string) (*types.WAFInstance, error) {
	instances, err := a.ListInstances(ctx, region)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.InstanceID == instanceID {
			return &inst, nil
		}
	}
	return nil, fmt.Errorf("WAF防护资源不存在: %s", instanceID)
}

// ListInstancesByIDs 批量获取WAF防护域名
func (a *WAFAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.WAFInstance, error) {
	var result []types.WAFInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取WAF防护资源失败", elog.String("instance_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取防护域名状态
func (a *WAFAdapter) GetInstanceStatus(ctx context.Context, region, instanceID string) (string, error) {
	inst, err := a.GetInstance(ctx, region, instanceID)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取WAF防护域名 + EO站点列表
// WAF DescribeDomains 和 EO DescribeZones 都是全局服务，不按 region 区分
func (a *WAFAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error) {
	var allInstances []types.WAFInstance

	// 1. 查询 WAF 防护域名（DescribeDomains）
	// WAF API 只支持 ap-guangzhou 地域，固定使用该 region
	wafDomains, err := a.listWAFDomains(ctx, "ap-guangzhou", filter)
	if err != nil {
		a.logger.Warn("获取腾讯云WAF防护域名失败", elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, wafDomains...)
	}

	// 2. 查询 EdgeOne(EO) 站点（DescribeZones）— 全局服务
	eoZones, err := a.listEOZones(ctx, filter)
	if err != nil {
		a.logger.Warn("获取腾讯云EO站点失败", elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, eoZones...)
	}

	a.logger.Info("获取腾讯云WAF防护资源列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// listWAFDomains 查询 WAF 防护域名
func (a *WAFAdapter) listWAFDomains(ctx context.Context, region string, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error) {
	client, err := a.createWAFClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建WAF客户端失败: %w", err)
	}

	var allInstances []types.WAFInstance
	offset := uint64(0)
	limit := uint64(100)

	for {
		request := waf.NewDescribeDomainsRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeDomains(request)
		if err != nil {
			return nil, fmt.Errorf("获取WAF防护域名列表失败: %w", err)
		}

		if response.Response.Domains == nil || len(response.Response.Domains) == 0 {
			break
		}

		for _, d := range response.Response.Domains {
			inst := a.convertWAFDomainToInstance(d, region)
			a.enrichWAFDomainSources(client, d, &inst)
			a.enrichWAFDomainRules(client, d, &inst)
			allInstances = append(allInstances, inst)
		}

		total := uint64(0)
		if response.Response.Total != nil {
			total = *response.Response.Total
		}
		if uint64(len(allInstances)) >= total || len(response.Response.Domains) < int(limit) {
			break
		}
		offset += limit
	}

	return allInstances, nil
}

// listEOZones 查询 EdgeOne 站点
func (a *WAFAdapter) listEOZones(ctx context.Context, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error) {
	client, err := a.createTEOClient()
	if err != nil {
		return nil, fmt.Errorf("创建TEO客户端失败: %w", err)
	}

	var allInstances []types.WAFInstance
	offset := int64(0)
	limit := int64(100)

	for {
		request := teo.NewDescribeZonesRequest()
		request.Offset = &offset
		request.Limit = &limit

		response, err := client.DescribeZones(request)
		if err != nil {
			return nil, fmt.Errorf("获取EO站点列表失败: %w", err)
		}

		if response.Response.Zones == nil || len(response.Response.Zones) == 0 {
			break
		}

		for _, zone := range response.Response.Zones {
			allInstances = append(allInstances, a.convertEOZoneToInstance(zone))
		}

		total := int64(0)
		if response.Response.TotalCount != nil {
			total = *response.Response.TotalCount
		}
		if int64(len(allInstances)) >= total || len(response.Response.Zones) < int(limit) {
			break
		}
		offset += limit
	}

	return allInstances, nil
}

// convertWAFDomainToInstance 将腾讯云 WAF 防护域名转换为通用 WAFInstance
func (a *WAFAdapter) convertWAFDomainToInstance(d *waf.DomainInfo, region string) types.WAFInstance {
	domain := ""
	if d.Domain != nil {
		domain = *d.Domain
	}
	domainID := ""
	if d.DomainId != nil {
		domainID = *d.DomainId
	}
	edition := ""
	if d.Edition != nil {
		edition = *d.Edition
	}

	status := "active"
	wafEnabled := true
	// State: 0=未防护 1=防护中
	if d.State != nil && *d.State == 0 {
		status = "suspended"
		wafEnabled = false
	}

	// 提取源站 IP(列表接口的 SrcList 可能精简;详情接口在 enrichWAFDomainSources 里回捞完整源站)
	var sourceIPs []string
	if d.SrcList != nil {
		for _, ip := range d.SrcList {
			if ip != nil && *ip != "" {
				sourceIPs = append(sourceIPs, *ip)
			}
		}
	}
	// 兜底补读域名回源列表(UpstreamDomainList)——即使详情接口失败也能多回捞一层
	if d.UpstreamDomainList != nil {
		for _, dm := range d.UpstreamDomainList {
			if dm != nil && *dm != "" {
				sourceIPs = append(sourceIPs, *dm)
			}
		}
	}

	cname := ""
	if d.Cname != nil {
		cname = *d.Cname
	}

	// 创建时间:列表接口 DescribeDomains 的 DomainInfo.CreateTime 即创建时间(字符串原样
	// 透传,与详情接口 DomainsPartInfo.CreateTime 同源;SaaS 详情回捞非空时会覆盖,见
	// mergeSaaSSources)。腾讯云 SDK 该字段无时区信息,按厂商原值保留,不做归一化。
	creationTime := ""
	if d.CreateTime != nil {
		creationTime = *d.CreateTime
	}

	// 标签:列表接口 DescribeDomains 的 DomainInfo.Labels 即 WAF 控制台「域名标签」,
	// 为自由文本(无键值结构);以标签本身为键、空字符串为值归入通用 Tags map,
	// 语义=仅标记标签存在性,不硬造值。列表层直接可用,无需 N+1。
	tags := make(map[string]string)
	if d.Labels != nil {
		for _, l := range d.Labels {
			if l != nil && *l != "" {
				tags[*l] = ""
			}
		}
	}

	return types.WAFInstance{
		InstanceID:     domainID,
		InstanceName:   domain,
		Status:         status,
		Region:         region,
		Edition:        edition,
		DomainCount:    1,
		ProtectedHosts: []string{domain},
		SourceIPs:      sourceIPs,
		Cname:          cname,
		WAFEnabled:     wafEnabled,
		Provider:       "tencent",
		CreationTime:   creationTime,
		Description:    "WAF防护域名",
		Tags:           tags,
	}
}

// enrichWAFDomainSources 回捞腾讯云 WAF 域名的完整源站信息(best-effort)。
// 列表接口 DescribeDomains 的 SrcList 可能精简;按 Edition 分派详情接口取完整源站:
//   - sparta-waf(SaaS 型)→ DescribeDomainDetailsSaas → DomainsPartInfo(SrcList + UpstreamDomain)
//   - clb-waf / cdc-clb-waf(CLB 型)→ DescribeDomainDetailsClb → ClbDomainsInfo(LoadBalancerSet 关联 LB)
//
// 详情失败/无结果时静默返回,保留列表接口已填的源站兜底。
func (a *WAFAdapter) enrichWAFDomainSources(client *waf.Client, d *waf.DomainInfo, inst *types.WAFInstance) {
	edition := ""
	if d.Edition != nil {
		edition = *d.Edition
	}
	req := waf.NewDescribeDomainDetailsSaasRequest()
	req.Domain = d.Domain
	req.DomainId = d.DomainId
	if d.InstanceId != nil {
		req.InstanceId = d.InstanceId
	}

	switch edition {
	case "sparta-waf":
		resp, err := client.DescribeDomainDetailsSaas(req)
		if err != nil {
			a.logger.Warn("获取腾讯云WAF SaaS域名详情失败", elog.String("domain", deref(d.Domain)), elog.FieldErr(err))
			return
		}
		if resp == nil || resp.Response == nil || resp.Response.DomainsPartInfo == nil {
			return
		}
		mergeSaaSSources(resp.Response.DomainsPartInfo, inst)
	case "clb-waf", "cdc-clb-waf":
		clbReq := waf.NewDescribeDomainDetailsClbRequest()
		clbReq.Domain = d.Domain
		clbReq.DomainId = d.DomainId
		if d.InstanceId != nil {
			clbReq.InstanceId = d.InstanceId
		}
		resp, err := client.DescribeDomainDetailsClb(clbReq)
		if err != nil {
			a.logger.Warn("获取腾讯云WAF CLB域名详情失败", elog.String("domain", deref(d.Domain)), elog.FieldErr(err))
			return
		}
		if resp == nil || resp.Response == nil || resp.Response.DomainsClbPartInfo == nil {
			return
		}
		mergeCLBSources(resp.Response.DomainsClbPartInfo, d, inst)
	default:
		// 未知 Edition(如未来新类型),不调详情接口,保留列表兜底
		return
	}
}

// deref 解引用 *string,空指针返回空字符串
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// enrichWAFDomainRules 回捞域名规则数(best-effort)。
// DescribeDomainRules 返回规则列表,规则数取其长度;规则明细列表(名称/动作/类型)
// 属二期「防护规则列表」,当前只落计数。
func (a *WAFAdapter) enrichWAFDomainRules(client *waf.Client, d *waf.DomainInfo, inst *types.WAFInstance) {
	if d.Domain == nil {
		return
	}
	req := waf.NewDescribeDomainRulesRequest()
	req.Domain = d.Domain
	resp, err := client.DescribeDomainRules(req)
	if err != nil {
		a.logger.Warn("获取腾讯云WAF域名规则失败", elog.String("domain", deref(d.Domain)), elog.FieldErr(err))
		return
	}
	if resp == nil || resp.Response == nil {
		return
	}
	inst.RuleCount = len(resp.Response.Rules)
	inst.Rules = convertTencentRules(resp.Response.Rules)
}

// convertTencentRules 将腾讯门神规则(Rule)统一归一到通用 WAFRule。
// 门神规则是全局同一套(每域 2000+ 条、跨域 99% 同构),全量落库会产生 25w+ 冗余行,
// 故只存预览前 maxPreviewRules 条,完整条数由 RuleCount 承载。
// Status 语义:0=禁用,其余(含 nil)视为启用(保守,不误标)。
func convertTencentRules(rules []*waf.Rule) []types.WAFRule {
	out := make([]types.WAFRule, 0, len(rules))
	for _, r := range rules {
		if r == nil {
			continue
		}
		status := "enabled"
		if r.Status != nil && *r.Status == 0 {
			status = "disabled"
		}
		out = append(out, types.WAFRule{
			Name:   deref(r.Description),
			Type:   deref(r.Type),
			Level:  deref(r.Level),
			Status: status,
		})
		if len(out) >= maxPreviewRules {
			break
		}
	}
	return out
}

// maxPreviewRules 门神规则预览条数上限(完整条数见 RuleCount)。
const maxPreviewRules = 100

// mergeSaaSSources 将 SaaS 型域名详情(DomainsPartInfo)的完整源站合并进 instance。
// SrcList = IP 回源(UpstreamType=0),UpstreamDomain = 域名回源(UpstreamType=1)。
func mergeSaaSSources(detail *waf.DomainsPartInfo, inst *types.WAFInstance) {
	var src []string
	if detail.SrcList != nil {
		for _, ip := range detail.SrcList {
			if ip != nil && *ip != "" {
				src = append(src, *ip)
			}
		}
	}
	if detail.UpstreamDomain != nil && *detail.UpstreamDomain != "" {
		src = append(src, *detail.UpstreamDomain)
	}
	if len(src) > 0 {
		inst.SourceIPs = src
	}
	if detail.Cname != nil && *detail.Cname != "" {
		inst.Cname = *detail.Cname
	}
	if detail.CreateTime != nil && *detail.CreateTime != "" {
		inst.CreationTime = *detail.CreateTime
	}
	// 防护模式:0=观察 1=拦截(SaaS 型域名详情带 Mode,CLB 型无)
	if detail.Mode != nil {
		if *detail.Mode == 1 {
			inst.ProtectionMode = "block"
		} else {
			inst.ProtectionMode = "observe"
		}
	}
	// 域名回源时标注,便于运营视图分辨回源类型
	if detail.UpstreamType != nil && *detail.UpstreamType == 1 && inst.Description == "WAF防护域名" {
		inst.Description = "WAF防护域名 upstream_type=domain"
	}
}

// mergeCLBSources 将 CLB 型域名详情(ClbDomainsInfo)的关联负载均衡信息合并进 instance。
// CLB 型 WAF 的源站语义是「挂在 CLB 上」而非直连 IP,故 SourceIPs 留空、关联 LB 写入 Description。
func mergeCLBSources(detail *waf.ClbDomainsInfo, d *waf.DomainInfo, inst *types.WAFInstance) {
	lbs := detail.LoadBalancerSet
	if len(lbs) == 0 {
		// 详情无关联 LB 时,回退到列表接口的 LoadBalancerSet
		lbs = d.LoadBalancerSet
	}
	if len(lbs) == 0 {
		return
	}
	var parts []string
	for _, lb := range lbs {
		if lb == nil {
			continue
		}
		name := deref(lb.LoadBalancerName)
		id := deref(lb.LoadBalancerId)
		listener := deref(lb.ListenerName)
		protocol := deref(lb.Protocol)
		desc := ""
		if name != "" || id != "" {
			desc = fmt.Sprintf("clb=%s(%s)", name, id)
		}
		if listener != "" {
			desc = fmt.Sprintf("%s listener=%s", desc, listener)
		}
		if protocol != "" {
			desc = fmt.Sprintf("%s protocol=%s", desc, protocol)
		}
		if desc != "" {
			parts = append(parts, desc)
		}
	}
	if len(parts) > 0 {
		inst.Description = "WAF防护域名(CLB) " + strings.Join(parts, "; ")
	}
}

// convertEOZoneToInstance 将腾讯云 EdgeOne 站点转换为通用 WAFInstance
func (a *WAFAdapter) convertEOZoneToInstance(zone *teo.Zone) types.WAFInstance {
	zoneID := ""
	if zone.ZoneId != nil {
		zoneID = *zone.ZoneId
	}
	zoneName := ""
	if zone.ZoneName != nil {
		zoneName = *zone.ZoneName
	}
	status := "active"
	if zone.Status != nil {
		// active / paused / deleted
		status = *zone.Status
	}
	area := ""
	if zone.Area != nil {
		area = *zone.Area
	}
	zoneType := ""
	if zone.Type != nil {
		zoneType = *zone.Type
	}
	createdOn := ""
	if zone.CreatedOn != nil {
		createdOn = *zone.CreatedOn
	}
	modifiedOn := ""
	if zone.ModifiedOn != nil {
		modifiedOn = *zone.ModifiedOn
	}

	// 标签:EO 站点列表 DescribeZones 的 Zone.Tags([]*Tag{TagKey,TagValue}) 直接给出
	// 键值标签,列表层即可取到,归一为通用 Tags map,无需 N+1。
	tags := make(map[string]string)
	if zone.Tags != nil {
		for _, t := range zone.Tags {
			if t == nil {
				continue
			}
			k, v := "", ""
			if t.TagKey != nil {
				k = *t.TagKey
			}
			if t.TagValue != nil {
				v = *t.TagValue
			}
			if k != "" {
				tags[k] = v
			}
		}
	}

	return types.WAFInstance{
		InstanceID:     zoneID,
		InstanceName:   zoneName,
		Status:         status,
		Edition:        "EdgeOne",
		DomainCount:    1,
		ProtectedHosts: []string{zoneName},
		WAFEnabled:     status == "active",
		Provider:       "tencent",
		CreationTime:   createdOn,
		Description:    fmt.Sprintf("EdgeOne站点 type=%s area=%s modified=%s", zoneType, area, modifiedOn),
		Tags:           tags,
	}
}
