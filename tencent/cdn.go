package tencent

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	tencentcdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// CDNAdapter 腾讯云CDN适配器
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
func (a *CDNAdapter) createClient() (*tencentcdn.Client, error) {
	credential := common.NewCredential(a.accessKeyID, a.accessKeySecret)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "cdn.tencentcloudapi.com"
	return tencentcdn.NewClient(credential, "", cpf)
}

// ListInstances 获取CDN加速域名列表
func (a *CDNAdapter) ListInstances(ctx context.Context, region string) ([]types.CDNInstance, error) {
	return a.ListInstancesWithFilter(ctx, region, nil)
}

// GetInstance 获取单个CDN加速域名详情
func (a *CDNAdapter) GetInstance(ctx context.Context, region, domainName string) (*types.CDNInstance, error) {
	client, err := a.createClient()
	if err != nil {
		return nil, fmt.Errorf("创建CDN客户端失败: %w", err)
	}

	request := tencentcdn.NewDescribeDomainsConfigRequest()
	request.Filters = []*tencentcdn.DomainFilter{
		{
			Name:  common.StringPtr("domain"),
			Value: common.StringPtrs([]string{domainName}),
		},
	}

	response, err := client.DescribeDomainsConfig(request)
	if err != nil {
		return nil, fmt.Errorf("获取CDN域名详情失败: %w", err)
	}

	if len(response.Response.Domains) == 0 {
		return nil, fmt.Errorf("CDN域名不存在: %s", domainName)
	}

	d := response.Response.Domains[0]
	instance := a.convertToInstance(d)
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
	offset := int64(0)
	limit := int64(100)

	if filter != nil && filter.PageSize > 0 {
		limit = int64(filter.PageSize)
	}

	for {
		request := tencentcdn.NewDescribeDomainsRequest()
		request.Offset = &offset
		request.Limit = &limit

		if filter != nil {
			var filters []*tencentcdn.DomainFilter
			if filter.DomainName != "" {
				filters = append(filters, &tencentcdn.DomainFilter{
					Name:  common.StringPtr("domain"),
					Value: common.StringPtrs([]string{filter.DomainName}),
				})
			}
			if filter.Status != "" {
				filters = append(filters, &tencentcdn.DomainFilter{
					Name:  common.StringPtr("status"),
					Value: common.StringPtrs([]string{filter.Status}),
				})
			}
			if len(filters) > 0 {
				request.Filters = filters
			}
		}

		response, err := client.DescribeDomains(request)
		if err != nil {
			return nil, fmt.Errorf("获取CDN域名列表失败: %w", err)
		}

		if response.Response.Domains == nil || len(response.Response.Domains) == 0 {
			break
		}

		for _, d := range response.Response.Domains {
			allInstances = append(allInstances, a.convertBriefToInstance(d))
		}

		if len(response.Response.Domains) < int(limit) {
			break
		}
		offset += limit
	}

	a.logger.Info("获取腾讯云CDN域名列表成功", elog.Int("count", len(allInstances)))
	return allInstances, nil
}

// convertToInstance 转换详情为通用CDN实例
func (a *CDNAdapter) convertToInstance(d *tencentcdn.DetailDomain) types.CDNInstance {
	domainName := ""
	if d.Domain != nil {
		domainName = *d.Domain
	}
	cname := ""
	if d.Cname != nil {
		cname = *d.Cname
	}
	status := ""
	if d.Status != nil {
		status = *d.Status
	}
	serviceType := ""
	if d.ServiceType != nil {
		serviceType = *d.ServiceType
	}
	area := ""
	if d.Area != nil {
		area = *d.Area
	}
	createTime := ""
	if d.CreateTime != nil {
		createTime = *d.CreateTime
	}
	updateTime := ""
	if d.UpdateTime != nil {
		updateTime = *d.UpdateTime
	}

	origins := make([]types.CDNOrigin, 0)
	originType := ""
	originHost := ""
	if d.Origin != nil {
		if d.Origin.Origins != nil {
			for _, o := range d.Origin.Origins {
				if o != nil {
					origins = append(origins, types.CDNOrigin{Address: *o, Type: "domain"})
				}
			}
		}
		if d.Origin.OriginType != nil {
			originType = *d.Origin.OriginType
			for i := range origins {
				origins[i].Type = originType
			}
		}
		if d.Origin.ServerName != nil {
			originHost = *d.Origin.ServerName
		}
	}

	httpsEnabled := false
	if d.Https != nil && d.Https.Switch != nil && *d.Https.Switch == "on" {
		httpsEnabled = true
	}

	projectID := ""
	if d.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *d.ProjectId)
	}

	domainID := ""
	if d.ResourceId != nil {
		domainID = *d.ResourceId
	}

	return types.CDNInstance{
		DomainID:     domainID,
		DomainName:   domainName,
		Cname:        cname,
		Status:       status,
		BusinessType: serviceType,
		ServiceArea:  area,
		Origins:      origins,
		OriginType:   originType,
		OriginHost:   originHost,
		HTTPSEnabled: httpsEnabled,
		CreationTime: createTime,
		ModifiedTime: updateTime,
		ProjectID:    projectID,
		Provider:     "tencent",
		Tags:         make(map[string]string),
	}
}

// convertBriefToInstance 转换简要信息为通用CDN实例
func (a *CDNAdapter) convertBriefToInstance(d *tencentcdn.BriefDomain) types.CDNInstance {
	domainName := ""
	if d.Domain != nil {
		domainName = *d.Domain
	}
	status := ""
	if d.Status != nil {
		status = *d.Status
	}
	serviceType := ""
	if d.ServiceType != nil {
		serviceType = *d.ServiceType
	}
	area := ""
	if d.Area != nil {
		area = *d.Area
	}
	createTime := ""
	if d.CreateTime != nil {
		createTime = *d.CreateTime
	}
	updateTime := ""
	if d.UpdateTime != nil {
		updateTime = *d.UpdateTime
	}
	cname := ""
	if d.Cname != nil {
		cname = *d.Cname
	}
	projectID := ""
	if d.ProjectId != nil {
		projectID = fmt.Sprintf("%d", *d.ProjectId)
	}

	// 提取源站信息
	origins := make([]types.CDNOrigin, 0)
	originType := ""
	originHost := ""
	if d.Origin != nil {
		if d.Origin.Origins != nil {
			for _, o := range d.Origin.Origins {
				if o != nil {
					origins = append(origins, types.CDNOrigin{Address: *o, Type: "domain"})
				}
			}
		}
		if d.Origin.OriginType != nil {
			originType = *d.Origin.OriginType
			// 更新源站类型
			for i := range origins {
				origins[i].Type = originType
			}
		}
		if d.Origin.ServerName != nil {
			originHost = *d.Origin.ServerName
		}
	}

	httpsEnabled := false
	domainID := ""
	if d.ResourceId != nil {
		domainID = *d.ResourceId
	}

	return types.CDNInstance{
		DomainID:     domainID,
		DomainName:   domainName,
		Cname:        cname,
		Status:       status,
		BusinessType: serviceType,
		ServiceArea:  area,
		Origins:      origins,
		OriginType:   originType,
		OriginHost:   originHost,
		HTTPSEnabled: httpsEnabled,
		CreationTime: createTime,
		ModifiedTime: updateTime,
		ProjectID:    projectID,
		Provider:     "tencent",
		Tags:         make(map[string]string),
	}
}
