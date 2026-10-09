package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/global"
	cdnv2 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
	cdnregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/region"
)

// CDNAdapter 华为云CDN适配器
type CDNAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewCDNAdapter 创建CDN适配器
func NewCDNAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *CDNAdapter {
	return &CDNAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建CDN客户端
// CDN 是全局服务，使用全局凭证 + cn-north-1 region（endpoint: cdn.myhuaweicloud.com）
func (a *CDNAdapter) createClient() (*cdnv2.CdnClient, error) {
	auth, err := global.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	client, err := cdnv2.CdnClientBuilder().
		WithRegion(cdnregion.CN_NORTH_1).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云CDN客户端失败: %w", err)
	}

	return cdnv2.NewCdnClient(client), nil
}

// ListInstances 获取CDN加速域名列表
func (a *CDNAdapter) ListInstances(ctx context.Context, region string) ([]types.CDNInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个CDN加速域名详情
func (a *CDNAdapter) GetInstance(ctx context.Context, region, domainName string) (*types.CDNInstance, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, err
	}

	request := &cdnmodel.ShowDomainDetailByNameRequest{
		DomainName: domainName,
	}

	response, err := client.ShowDomainDetailByName(request)
	if err != nil {
		return nil, fmt.Errorf("获取CDN域名详情失败: %w", err)
	}

	if response.Domain == nil {
		return nil, fmt.Errorf("CDN域名不存在: %s", domainName)
	}

	d := response.Domain
	instance := a.convertDetailToInstance(d)
	return &instance, nil
}

// ListInstancesByIDs 批量获取CDN加速域名
func (a *CDNAdapter) ListInstancesByIDs(ctx context.Context, region string, domainNames []string) ([]types.CDNInstance, error) {
	var result []types.CDNInstance
	for _, name := range domainNames {
		inst, err := a.GetInstance(ctx, region, name)
		if err != nil {
			a.logger.Warn("获取CDN域名失败", elog.String("domain", name), elog.FieldErr(err))
			continue
		}
		result = append(result, *inst)
	}
	return result, nil
}

// GetInstanceStatus 获取域名状态
func (a *CDNAdapter) GetInstanceStatus(ctx context.Context, region, domainName string) (string, error) {
	inst, err := a.GetInstance(ctx, region, domainName)
	if err != nil {
		return "", err
	}
	return inst.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取域名列表
func (a *CDNAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.CDNInstanceFilter) ([]types.CDNInstance, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("创建CDN客户端失败: %w", err)
	}

	var allInstances []types.CDNInstance
	page := int32(1)
	pageSize := int32(100)

	if filter != nil && filter.PageSize > 0 {
		pageSize = int32(filter.PageSize)
	}

	for {
		// enterprise_project_id 设为 "all" 查询所有企业项目的域名
		// 华为云子账号调用时此参数必传
		allProjects := "all"
		request := &cdnmodel.ListDomainsRequest{
			PageNumber:          &page,
			PageSize:            &pageSize,
			EnterpriseProjectId: &allProjects,
		}

		if filter != nil {
			if filter.DomainName != "" {
				request.DomainName = &filter.DomainName
			}
			if filter.Status != "" {
				request.DomainStatus = &filter.Status
			}
			if filter.BusinessType != "" {
				request.BusinessType = &filter.BusinessType
			}
		}

		response, err := client.ListDomains(request)
		if err != nil {
			return nil, fmt.Errorf("获取CDN域名列表失败: %w", err)
		}

		if response.Domains == nil || len(*response.Domains) == 0 {
			break
		}

		for _, d := range *response.Domains {
			allInstances = append(allInstances, a.convertToInstance(d))
		}

		if len(*response.Domains) < int(pageSize) {
			break
		}
		page++
	}

	a.logger.Info("获取华为云CDN域名列表成功", elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// convertToInstance 转换列表项为通用CDN实例
func (a *CDNAdapter) convertToInstance(d cdnmodel.Domains) types.CDNInstance {
	domainName := ""
	if d.DomainName != nil {
		domainName = *d.DomainName
	}
	cname := ""
	if d.Cname != nil {
		cname = *d.Cname
	}
	status := ""
	if d.DomainStatus != nil {
		status = *d.DomainStatus
	}
	businessType := ""
	if d.BusinessType != nil {
		businessType = *d.BusinessType
	}
	serviceArea := ""
	if d.ServiceArea != nil {
		serviceArea = string(d.ServiceArea.Value())
	}
	createTime := ""
	if d.CreateTime != nil {
		createTime = fmt.Sprintf("%d", *d.CreateTime)
	}
	modifyTime := ""
	if d.ModifyTime != nil {
		modifyTime = fmt.Sprintf("%d", *d.ModifyTime)
	}
	domainID := ""
	if d.Id != nil {
		domainID = *d.Id
	}
	httpsEnabled := false
	if d.HttpsStatus != nil && *d.HttpsStatus == 2 {
		httpsEnabled = true
	}

	origins := make([]types.CDNOrigin, 0)
	if d.Sources != nil {
		for _, src := range *d.Sources {
			origin := types.CDNOrigin{
				Address: src.IpOrDomain,
				Type:    string(src.OriginType.Value()),
			}
			if src.ActiveStandby == 0 {
				origin.Priority = 1
			} else {
				origin.Priority = 2
			}
			origins = append(origins, origin)
		}
	}

	return types.CDNInstance{
		DomainID:     domainID,
		DomainName:   domainName,
		Cname:        cname,
		Status:       status,
		BusinessType: businessType,
		ServiceArea:  serviceArea,
		Origins:      origins,
		OriginType:   a.inferOriginType(origins),
		OriginHost:   a.inferOriginHost(origins),
		HTTPSEnabled: httpsEnabled,
		CreationTime: createTime,
		ModifiedTime: modifyTime,
		Provider:     "huawei",
		Tags:         make(map[string]string),
	}
}

// convertDetailToInstance 转换详情为通用CDN实例
func (a *CDNAdapter) convertDetailToInstance(d *cdnmodel.DomainsDetail) types.CDNInstance {
	domainName := ""
	if d.DomainName != nil {
		domainName = *d.DomainName
	}
	cname := ""
	if d.Cname != nil {
		cname = *d.Cname
	}
	status := ""
	if d.DomainStatus != nil {
		status = *d.DomainStatus
	}
	businessType := ""
	if d.BusinessType != nil {
		businessType = *d.BusinessType
	}
	serviceArea := ""
	if d.ServiceArea != nil {
		serviceArea = string(d.ServiceArea.Value())
	}
	createTime := ""
	if d.CreateTime != nil {
		createTime = fmt.Sprintf("%d", *d.CreateTime)
	}
	domainID := ""
	if d.Id != nil {
		domainID = *d.Id
	}
	httpsEnabled := false
	if d.HttpsStatus != nil && *d.HttpsStatus == 2 {
		httpsEnabled = true
	}

	origins := make([]types.CDNOrigin, 0)
	if d.Sources != nil {
		for _, src := range *d.Sources {
			origin := types.CDNOrigin{
				Address: src.OriginAddr,
				Type:    src.OriginType,
			}
			origins = append(origins, origin)
		}
	}

	return types.CDNInstance{
		DomainID:     domainID,
		DomainName:   domainName,
		Cname:        cname,
		Status:       status,
		BusinessType: businessType,
		ServiceArea:  serviceArea,
		Origins:      origins,
		OriginType:   a.inferOriginType(origins),
		OriginHost:   a.inferOriginHost(origins),
		HTTPSEnabled: httpsEnabled,
		CreationTime: createTime,
		Provider:     "huawei",
		Tags:         make(map[string]string),
	}
}

// inferOriginType 推断源站类型
func (a *CDNAdapter) inferOriginType(origins []types.CDNOrigin) string {
	if len(origins) > 0 {
		return origins[0].Type
	}
	return ""
}

// inferOriginHost 推断源站地址
func (a *CDNAdapter) inferOriginHost(origins []types.CDNOrigin) string {
	if len(origins) > 0 {
		return origins[0].Address
	}
	return ""
}
