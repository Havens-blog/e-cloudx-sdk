package huawei

import (
	"context"
	"fmt"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	wafv1 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1"
	wafmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/model"
	wafregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/region"
)

// WAFAdapter 华为云WAF适配器
// 同步的是 Web 应用防火墙的防护域名（ListHost），不是独享引擎实例（ListInstance）
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

// createClient 创建WAF客户端
func (a *WAFAdapter) createClient(reg string) (*wafv1.WafClient, error) {
	if reg == "" {
		reg = a.defaultRegion
	}

	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	r, err := wafregion.SafeValueOf(reg)
	if err != nil {
		return nil, fmt.Errorf("无效的WAF地域: %s", reg)
	}

	client, err := wafv1.WafClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云WAF客户端失败: %w", err)
	}

	return wafv1.NewWafClient(client), nil
}

// ListInstances 获取WAF防护域名列表
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
	return nil, fmt.Errorf("WAF防护域名不存在: %s", instanceID)
}

// ListInstancesByIDs 批量获取WAF防护域名
func (a *WAFAdapter) ListInstancesByIDs(ctx context.Context, region string, instanceIDs []string) ([]types.WAFInstance, error) {
	var result []types.WAFInstance
	for _, id := range instanceIDs {
		inst, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取WAF防护域名失败", elog.String("instance_id", id), elog.FieldErr(err))
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

// ListInstancesWithFilter 带过滤条件获取WAF防护域名列表
// 使用 ListHost API 查询云模式下的防护域名
func (a *WAFAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.WAFInstanceFilter) ([]types.WAFInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建WAF客户端失败: %w", err)
	}

	var allInstances []types.WAFInstance
	page := int32(1)
	pageSize := int32(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int32(filter.PageSize)
	}

	for {
		// enterprise_project_id 设为 "all_granted_eps" 查询所有企业项目的防护域名
		allProjects := "all_granted_eps"
		request := &wafmodel.ListHostRequest{
			Page:                &page,
			Pagesize:            &pageSize,
			EnterpriseProjectId: &allProjects,
		}

		if filter != nil && filter.InstanceName != "" {
			request.Hostname = &filter.InstanceName
		}

		response, err := client.ListHost(request)
		if err != nil {
			return nil, fmt.Errorf("获取WAF防护域名列表失败: %w", err)
		}

		if response.Items == nil || len(*response.Items) == 0 {
			break
		}

		for _, item := range *response.Items {
			allInstances = append(allInstances, a.convertHostToInstance(item, region))
		}

		if len(*response.Items) < int(pageSize) {
			break
		}
		page++
	}

	a.logger.Info("获取华为云WAF防护域名列表成功",
		elog.String("region", region),
		elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// convertHostToInstance 将华为云 WAF 防护域名转换为通用 WAFInstance
func (a *WAFAdapter) convertHostToInstance(item wafmodel.CloudWafHostItem, region string) types.WAFInstance {
	hostID := ""
	if item.Id != nil {
		hostID = *item.Id
	}
	hostname := ""
	if item.Hostname != nil {
		hostname = *item.Hostname
	}
	description := ""
	if item.Description != nil {
		description = *item.Description
	}
	itemRegion := region
	if item.Region != nil && *item.Region != "" {
		itemRegion = *item.Region
	}

	// 防护状态: -1=bypass, 0=暂停防护, 1=开启防护
	status := "active"
	wafEnabled := true
	if item.ProtectStatus != nil {
		switch *item.ProtectStatus {
		case -1:
			status = "bypass"
			wafEnabled = false
		case 0:
			status = "suspended"
			wafEnabled = false
		case 1:
			status = "active"
			wafEnabled = true
		}
	}

	// 接入状态: 0=未接入, 1=已接入
	accessStatus := ""
	if item.AccessStatus != nil {
		if *item.AccessStatus == 1 {
			accessStatus = "connected"
		} else {
			accessStatus = "disconnected"
		}
	}

	exclusiveIP := false
	if item.ExclusiveIp != nil {
		exclusiveIP = *item.ExclusiveIp
	}

	creationTime := ""
	if item.Timestamp != nil {
		creationTime = time.UnixMilli(*item.Timestamp).Format("2006-01-02T15:04:05Z")
	}

	payType := ""
	if item.PaidType != nil {
		payType = string(item.PaidType.Value())
	}

	webTag := ""
	if item.WebTag != nil {
		webTag = *item.WebTag
	}

	policyID := ""
	if item.Policyid != nil {
		policyID = *item.Policyid
	}

	accessCode := ""
	if item.AccessCode != nil {
		accessCode = *item.AccessCode
	}

	enterpriseProjectID := ""
	if item.EnterpriseProjectId != nil {
		enterpriseProjectID = *item.EnterpriseProjectId
	}

	// 提取源站 IP
	var sourceIPs []string
	if item.Server != nil {
		for _, srv := range *item.Server {
			if srv.Address != "" {
				sourceIPs = append(sourceIPs, srv.Address)
			}
		}
	}

	// 构建 CNAME: 华为云 WAF 有两套 CNAME，始终两个都存
	// old: {accessCode}.waf.huaweicloud.com
	// new: {accessCode}.vip1.huaweicloudwaf.com
	cname := ""
	if accessCode != "" {
		cnameOld := fmt.Sprintf("%s.waf.huaweicloud.com", accessCode)
		cnameNew := fmt.Sprintf("%s.vip1.huaweicloudwaf.com", accessCode)
		cname = cnameOld + ";" + cnameNew
	}

	return types.WAFInstance{
		InstanceID:      hostID,
		InstanceName:    hostname,
		Status:          status,
		Region:          itemRegion,
		DomainCount:     1,
		ProtectedHosts:  []string{hostname},
		SourceIPs:       sourceIPs,
		Cname:           cname,
		WAFEnabled:      wafEnabled,
		ExclusiveIP:     exclusiveIP,
		PayType:         payType,
		CreationTime:    creationTime,
		ProjectID:       enterpriseProjectID,
		ResourceGroupID: enterpriseProjectID,
		Description:     fmt.Sprintf("%s access=%s web_tag=%s policy=%s", description, accessStatus, webTag, policyID),
		Provider:        "huawei",
		// Tags:已核实 SDK struct(huaweicloud-sdk-go-v3 waf v1)——CloudWafHostItem(ListHost
		// 列表项)与 ShowHost(详情)均无标签字段,仅 WebTag 网站名称(已并入 Description);
		// WAF v1 的 ListInstanceTags 虽可取标签,但 resourceid 要求「引擎 id」(云模式引擎/独享
		// 引擎),防护域名列表不返回引擎 id,且引擎级标签语义 ≠ 域名级标签。为避免语义错配与
		// 逐域名 N+1 的失控开销,不做回捞,留空 map,不硬造值。
		Tags: make(map[string]string),
	}
}
