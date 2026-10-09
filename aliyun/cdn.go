package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cdn"
	"github.com/gotomicro/ego/core/elog"
)

// CDNAdapter 阿里云CDN适配器
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
func (a *CDNAdapter) createClient() (*cdn.Client, error) {
	return cdn.NewClientWithAccessKey(a.defaultRegion, a.accessKeyID, a.accessKeySecret)
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

	request := cdn.CreateDescribeCdnDomainDetailRequest()
	request.DomainName = domainName

	response, err := client.DescribeCdnDomainDetail(request)
	if err != nil {
		return nil, fmt.Errorf("获取CDN域名详情失败: %w", err)
	}

	detail := response.GetDomainDetailModel
	origins := make([]types.CDNOrigin, 0)
	for _, src := range detail.SourceModels.SourceModel {
		priority, _ := strconv.Atoi(src.Priority)
		weight, _ := strconv.Atoi(src.Weight)
		origins = append(origins, types.CDNOrigin{
			Address:  src.Content,
			Type:     src.Type,
			Port:     src.Port,
			Priority: priority,
			Weight:   weight,
		})
	}

	originType := ""
	originHost := ""
	if len(origins) > 0 {
		originType = origins[0].Type
		originHost = origins[0].Address
	}

	httpsEnabled := detail.ServerCertificateStatus == "on"

	instance := types.CDNInstance{
		DomainName:      detail.DomainName,
		Cname:           detail.Cname,
		Status:          detail.DomainStatus,
		BusinessType:    detail.CdnType,
		ServiceArea:     detail.Scope,
		Origins:         origins,
		OriginType:      originType,
		OriginHost:      originHost,
		HTTPSEnabled:    httpsEnabled,
		ResourceGroupID: detail.ResourceGroupId,
		CreationTime:    detail.GmtCreated,
		ModifiedTime:    detail.GmtModified,
		Description:     detail.Description,
		Provider:        "aliyun",
		Tags:            make(map[string]string),
	}

	// 获取标签
	tags := a.getTagsForDomain(client, domainName)
	if tags != nil {
		instance.Tags = tags
	}

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
	pageNumber := 1
	pageSize := 50

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}

	for {
		request := cdn.CreateDescribeUserDomainsRequest()
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		if filter != nil {
			if filter.DomainName != "" {
				request.DomainName = filter.DomainName
			}
			if filter.Status != "" {
				request.DomainStatus = filter.Status
			}
		}

		response, err := client.DescribeUserDomains(request)
		if err != nil {
			return nil, fmt.Errorf("获取CDN域名列表失败: %w", err)
		}

		for _, d := range response.Domains.PageData {
			origins := make([]types.CDNOrigin, 0)
			for _, src := range d.Sources.Source {
				priority, _ := strconv.Atoi(src.Priority)
				weight, _ := strconv.Atoi(src.Weight)
				origins = append(origins, types.CDNOrigin{
					Address:  src.Content,
					Type:     src.Type,
					Port:     src.Port,
					Priority: priority,
					Weight:   weight,
				})
			}

			// 推断源站类型
			originType := ""
			originHost := ""
			if len(origins) > 0 {
				originType = origins[0].Type
				originHost = origins[0].Address
			}

			httpsEnabled := d.SslProtocol == "on"

			allInstances = append(allInstances, types.CDNInstance{
				DomainName:      d.DomainName,
				DomainID:        strconv.FormatInt(d.DomainId, 10),
				Cname:           d.Cname,
				Status:          d.DomainStatus,
				Region:          d.Coverage,
				BusinessType:    d.CdnType,
				ServiceArea:     d.Coverage,
				Origins:         origins,
				OriginType:      originType,
				OriginHost:      originHost,
				HTTPSEnabled:    httpsEnabled,
				ResourceGroupID: d.ResourceGroupId,
				CreationTime:    d.GmtCreated,
				ModifiedTime:    d.GmtModified,
				Description:     d.Description,
				Provider:        "aliyun",
				Tags:            make(map[string]string),
			})
		}

		if len(response.Domains.PageData) < pageSize {
			break
		}
		pageNumber++
	}

	// 批量获取标签信息
	if len(allInstances) > 0 {
		a.fillTags(client, allInstances)
	}

	a.logger.Info("获取阿里云CDN域名列表成功", elog.Int("count", len(allInstances)))

	// 同时获取全站加速(DCDN)域名
	dcdnInstances, err := a.listDCDNInstances(filter)
	if err != nil {
		a.logger.Warn("获取阿里云DCDN域名列表失败，跳过", elog.FieldErr(err))
	} else {
		allInstances = append(allInstances, dcdnInstances...)
		a.logger.Info("获取阿里云DCDN域名列表成功", elog.Int("count", len(dcdnInstances)))
	}

	return allInstances, nil
}

// fillTags 批量获取CDN域名的标签信息
func (a *CDNAdapter) fillTags(client *cdn.Client, instances []types.CDNInstance) {
	// DescribeTagResources 每次最多查询 50 个资源
	const batchSize = 50
	for i := 0; i < len(instances); i += batchSize {
		end := i + batchSize
		if end > len(instances) {
			end = len(instances)
		}

		resourceIDs := make([]string, 0, end-i)
		for _, inst := range instances[i:end] {
			resourceIDs = append(resourceIDs, inst.DomainName)
		}

		request := cdn.CreateDescribeTagResourcesRequest()
		request.ResourceType = "DOMAIN"
		request.ResourceId = &resourceIDs

		response, err := client.DescribeTagResources(request)
		if err != nil {
			a.logger.Warn("获取CDN标签失败", elog.FieldErr(err))
			continue
		}

		// 构建域名 -> 标签映射
		tagMap := make(map[string]map[string]string)
		for _, tr := range response.TagResources {
			tags := make(map[string]string)
			for _, t := range tr.Tag {
				tags[t.Key] = t.Value
			}
			tagMap[tr.ResourceId] = tags
		}

		// 回填标签
		for j := i; j < end; j++ {
			if tags, ok := tagMap[instances[j].DomainName]; ok {
				instances[j].Tags = tags
			}
		}
	}
}

// getTagsForDomain 获取单个域名的标签
func (a *CDNAdapter) getTagsForDomain(client *cdn.Client, domainName string) map[string]string {
	request := cdn.CreateDescribeTagResourcesRequest()
	request.ResourceType = "DOMAIN"
	request.ResourceId = &[]string{domainName}

	response, err := client.DescribeTagResources(request)
	if err != nil {
		a.logger.Warn("获取CDN域名标签失败", elog.String("domain", domainName), elog.FieldErr(err))
		return nil
	}

	tags := make(map[string]string)
	for _, tr := range response.TagResources {
		for _, t := range tr.Tag {
			tags[t.Key] = t.Value
		}
	}
	return tags
}

// ==================== DCDN (全站加速) ====================

// dcdnListResponse 阿里云 DCDN DescribeDcdnUserDomains 响应
type dcdnListResponse struct {
	RequestID  string `json:"RequestId"`
	PageNumber int64  `json:"PageNumber"`
	PageSize   int64  `json:"PageSize"`
	TotalCount int64  `json:"TotalCount"`
	Domains    struct {
		PageData []dcdnDomain `json:"PageData"`
	} `json:"Domains"`
}

type dcdnDomain struct {
	DomainName      string `json:"DomainName"`
	Cname           string `json:"Cname"`
	DomainStatus    string `json:"DomainStatus"`
	Description     string `json:"Description"`
	SSLProtocol     string `json:"SSLProtocol"` // on/off
	ResourceGroupID string `json:"ResourceGroupId"`
	GmtCreated      string `json:"GmtCreated"`
	GmtModified     string `json:"GmtModified"`
	Sources         struct {
		Source []dcdnSource `json:"Source"`
	} `json:"Sources"`
}

type dcdnSource struct {
	Content  string `json:"Content"` // 源站地址
	Type     string `json:"Type"`    // ipaddr/domain/oss
	Port     int    `json:"Port"`
	Priority string `json:"Priority"` // 20=主, 30=备
	Weight   string `json:"Weight"`
}

// listDCDNInstances 获取全站加速(DCDN)域名列表
func (a *CDNAdapter) listDCDNInstances(filter *types.CDNInstanceFilter) ([]types.CDNInstance, error) {
	client, err := sdk.NewClientWithAccessKey(a.defaultRegion, a.accessKeyID, a.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建DCDN客户端失败: %w", err)
	}

	var allInstances []types.CDNInstance
	pageNumber := 1
	pageSize := 100

	if filter != nil && filter.PageSize > 0 {
		pageSize = filter.PageSize
	}

	for {
		request := requests.NewCommonRequest()
		request.Method = "POST"
		request.Scheme = "https"
		request.Domain = "dcdn.aliyuncs.com"
		request.Version = "2018-01-15"
		request.ApiName = "DescribeDcdnUserDomains"
		request.QueryParams["PageNumber"] = strconv.Itoa(pageNumber)
		request.QueryParams["PageSize"] = strconv.Itoa(pageSize)

		if filter != nil {
			if filter.DomainName != "" {
				request.QueryParams["DomainName"] = filter.DomainName
			}
			if filter.Status != "" {
				request.QueryParams["DomainStatus"] = filter.Status
			}
		}

		response, err := client.ProcessCommonRequest(request)
		if err != nil {
			return nil, fmt.Errorf("获取DCDN域名列表失败: %w", err)
		}

		var resp dcdnListResponse
		if err := json.Unmarshal(response.GetHttpContentBytes(), &resp); err != nil {
			return nil, fmt.Errorf("解析DCDN响应失败: %w", err)
		}

		for _, d := range resp.Domains.PageData {
			allInstances = append(allInstances, a.convertDCDNToInstance(d))
		}

		if int64(len(allInstances)) >= resp.TotalCount || len(resp.Domains.PageData) < pageSize {
			break
		}
		pageNumber++
	}

	return allInstances, nil
}

// convertDCDNToInstance 转换DCDN域名为通用CDN实例
func (a *CDNAdapter) convertDCDNToInstance(d dcdnDomain) types.CDNInstance {
	origins := make([]types.CDNOrigin, 0, len(d.Sources.Source))
	for _, src := range d.Sources.Source {
		priority := 1
		if src.Priority == "30" {
			priority = 2
		}
		origins = append(origins, types.CDNOrigin{
			Address:  src.Content,
			Type:     src.Type,
			Port:     src.Port,
			Priority: priority,
		})
	}

	originType := ""
	originHost := ""
	if len(origins) > 0 {
		originType = origins[0].Type
		originHost = origins[0].Address
	}

	httpsEnabled := d.SSLProtocol == "on"

	return types.CDNInstance{
		DomainName:      d.DomainName,
		Cname:           d.Cname,
		Status:          d.DomainStatus,
		BusinessType:    "dcdn",
		Origins:         origins,
		OriginType:      originType,
		OriginHost:      originHost,
		HTTPSEnabled:    httpsEnabled,
		ResourceGroupID: d.ResourceGroupID,
		CreationTime:    d.GmtCreated,
		ModifiedTime:    d.GmtModified,
		Description:     "全站加速(DCDN)",
		Provider:        "aliyun",
		Tags:            make(map[string]string),
	}
}
