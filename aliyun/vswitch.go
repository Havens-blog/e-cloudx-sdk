package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	vpc "github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/gotomicro/ego/core/elog"
)

// VSwitchAdapter 阿里云VSwitch适配器
type VSwitchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewVSwitchAdapter 创建VSwitch适配器
func NewVSwitchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *VSwitchAdapter {
	return &VSwitchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建VPC客户端（VSwitch属于VPC服务）
func (a *VSwitchAdapter) createClient(region string) (*vpc.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return vpc.NewClientWithAccessKey(region, a.accessKeyID, a.accessKeySecret)
}

// ListInstances 获取VSwitch列表
func (a *VSwitchAdapter) ListInstances(ctx context.Context, region string) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	var allVSwitches []types.VSwitchInstance
	pageNumber := 1
	pageSize := 50

	for {
		request := vpc.CreateDescribeVSwitchesRequest()
		request.RegionId = region
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)

		response, err := client.DescribeVSwitches(request)
		if err != nil {
			return nil, fmt.Errorf("获取VSwitch列表失败: %w", err)
		}

		for _, vs := range response.VSwitches.VSwitch {
			allVSwitches = append(allVSwitches, a.convertToVSwitchInstance(vs, region))
		}

		if len(response.VSwitches.VSwitch) < pageSize {
			break
		}
		pageNumber++
	}

	a.logger.Info("获取阿里云VSwitch列表成功",
		elog.String("region", region),
		elog.Int("count", len(allVSwitches)))

	return allVSwitches, nil
}

// GetInstance 获取单个VSwitch详情
func (a *VSwitchAdapter) GetInstance(ctx context.Context, region, vswitchID string) (*types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	request := vpc.CreateDescribeVSwitchesRequest()
	request.RegionId = region
	request.VSwitchId = vswitchID

	response, err := client.DescribeVSwitches(request)
	if err != nil {
		return nil, fmt.Errorf("获取VSwitch详情失败: %w", err)
	}

	if len(response.VSwitches.VSwitch) == 0 {
		return nil, fmt.Errorf("VSwitch不存在: %s", vswitchID)
	}

	instance := a.convertToVSwitchInstance(response.VSwitches.VSwitch[0], region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取VSwitch
func (a *VSwitchAdapter) ListInstancesByIDs(ctx context.Context, region string, vswitchIDs []string) ([]types.VSwitchInstance, error) {
	var result []types.VSwitchInstance
	for _, id := range vswitchIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取VSwitch失败", elog.String("vswitch_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
	}
	return result, nil
}

// GetInstanceStatus 获取VSwitch状态
func (a *VSwitchAdapter) GetInstanceStatus(ctx context.Context, region, vswitchID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, vswitchID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取VSwitch列表
func (a *VSwitchAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.VSwitchInstanceFilter) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, fmt.Errorf("创建VPC客户端失败: %w", err)
	}

	request := vpc.CreateDescribeVSwitchesRequest()
	request.RegionId = region

	if filter != nil {
		if len(filter.VSwitchIDs) == 1 {
			request.VSwitchId = filter.VSwitchIDs[0]
		}
		if filter.VSwitchName != "" {
			request.VSwitchName = filter.VSwitchName
		}
		if filter.VPCID != "" {
			request.VpcId = filter.VPCID
		}
		if filter.Zone != "" {
			request.ZoneId = filter.Zone
		}
		if filter.IsDefault != nil {
			request.IsDefault = requests.NewBoolean(*filter.IsDefault)
		}
		if filter.PageNumber > 0 {
			request.PageNumber = requests.NewInteger(filter.PageNumber)
		}
		if filter.PageSize > 0 {
			request.PageSize = requests.NewInteger(filter.PageSize)
		}
	}

	response, err := client.DescribeVSwitches(request)
	if err != nil {
		return nil, fmt.Errorf("获取VSwitch列表失败: %w", err)
	}

	var result []types.VSwitchInstance
	for _, vs := range response.VSwitches.VSwitch {
		result = append(result, a.convertToVSwitchInstance(vs, region))
	}

	return result, nil
}

// convertToVSwitchInstance 转换为通用VSwitch实例
func (a *VSwitchAdapter) convertToVSwitchInstance(vs vpc.VSwitch, region string) types.VSwitchInstance {
	tags := make(map[string]string)
	for _, tag := range vs.Tags.Tag {
		tags[tag.Key] = tag.Value
	}

	return types.VSwitchInstance{
		VSwitchID:        vs.VSwitchId,
		VSwitchName:      vs.VSwitchName,
		Status:           types.NormalizeVSwitchStatus("aliyun", vs.Status),
		Region:           region,
		Zone:             vs.ZoneId,
		Description:      vs.Description,
		CidrBlock:        vs.CidrBlock,
		IPv6CidrBlock:    vs.Ipv6CidrBlock,
		IsDefault:        vs.IsDefault,
		VPCID:            vs.VpcId,
		AvailableIPCount: vs.AvailableIpAddressCount,
		CreationTime:     vs.CreationTime,
		ResourceGroupID:  vs.ResourceGroupId,
		Tags:             tags,
		Provider:         "aliyun",
	}
}
