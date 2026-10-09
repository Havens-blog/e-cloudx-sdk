package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	vpcClient "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/model"
	vpcregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/region"
)

// VSwitchAdapter 华为云子网适配器
type VSwitchAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewVSwitchAdapter 创建子网适配器
func NewVSwitchAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *VSwitchAdapter {
	return &VSwitchAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建VPC客户端
func (a *VSwitchAdapter) createClient(region string) (*vpcClient.VpcClient, error) {
	if region == "" {
		region = a.defaultRegion
	}

	auth, err := basic.NewCredentialsBuilder().
		WithAk(a.accessKeyID).
		WithSk(a.accessKeySecret).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云凭证失败: %w", err)
	}

	r, err := vpcregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的地域: %s", region)
	}

	client, err := vpcClient.VpcClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云VPC客户端失败: %w", err)
	}

	return vpcClient.NewVpcClient(client), nil
}

// ListInstances 获取子网列表
func (a *VSwitchAdapter) ListInstances(ctx context.Context, region string) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allVSwitches []types.VSwitchInstance
	var marker *string
	limit := int32(100)

	for {
		request := &model.ListSubnetsRequest{
			Limit:  &limit,
			Marker: marker,
		}

		response, err := client.ListSubnets(request)
		if err != nil {
			return nil, fmt.Errorf("获取子网列表失败: %w", err)
		}

		if response.Subnets == nil {
			break
		}

		for _, s := range *response.Subnets {
			allVSwitches = append(allVSwitches, a.convertToVSwitchInstance(s, region))
		}

		if len(*response.Subnets) < int(limit) {
			break
		}
		lastSubnet := (*response.Subnets)[len(*response.Subnets)-1]
		marker = &lastSubnet.Id
	}

	a.logger.Info("获取华为云子网列表成功",
		elog.String("region", region),
		elog.Int("count", len(allVSwitches)))

	return allVSwitches, nil
}

// GetInstance 获取单个子网详情
func (a *VSwitchAdapter) GetInstance(ctx context.Context, region, vswitchID string) (*types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ShowSubnetRequest{
		SubnetId: vswitchID,
	}

	response, err := client.ShowSubnet(request)
	if err != nil {
		return nil, fmt.Errorf("获取子网详情失败: %w", err)
	}

	if response.Subnet == nil {
		return nil, fmt.Errorf("子网不存在: %s", vswitchID)
	}

	instance := a.convertDetailToVSwitchInstance(*response.Subnet, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取子网
func (a *VSwitchAdapter) ListInstancesByIDs(ctx context.Context, region string, vswitchIDs []string) ([]types.VSwitchInstance, error) {
	var result []types.VSwitchInstance
	for _, id := range vswitchIDs {
		instance, err := a.GetInstance(ctx, region, id)
		if err != nil {
			a.logger.Warn("获取子网失败", elog.String("vswitch_id", id), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
	}
	return result, nil
}

// GetInstanceStatus 获取子网状态
func (a *VSwitchAdapter) GetInstanceStatus(ctx context.Context, region, vswitchID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, vswitchID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取子网列表
func (a *VSwitchAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.VSwitchInstanceFilter) ([]types.VSwitchInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ListSubnetsRequest{}

	if filter != nil {
		if filter.VPCID != "" {
			request.VpcId = &filter.VPCID
		}
		if filter.PageSize > 0 {
			limit := int32(filter.PageSize)
			request.Limit = &limit
		}
	}

	response, err := client.ListSubnets(request)
	if err != nil {
		return nil, fmt.Errorf("获取子网列表失败: %w", err)
	}

	var result []types.VSwitchInstance
	if response.Subnets != nil {
		for _, s := range *response.Subnets {
			result = append(result, a.convertToVSwitchInstance(s, region))
		}
	}

	return result, nil
}

// convertToVSwitchInstance 转换为通用子网实例
func (a *VSwitchAdapter) convertToVSwitchInstance(s model.Subnet, region string) types.VSwitchInstance {
	status := "Available"
	if s.Status.Value() != "" {
		status = s.Status.Value()
	}

	tags := make(map[string]string)

	return types.VSwitchInstance{
		VSwitchID:   s.Id,
		VSwitchName: s.Name,
		Status:      types.NormalizeVSwitchStatus("huawei", status),
		Region:      region,
		Zone:        s.AvailabilityZone,
		Description: s.Description,
		CidrBlock:   s.Cidr,
		GatewayIP:   s.GatewayIp,
		VPCID:       s.VpcId,
		Tags:        tags,
		Provider:    "huawei",
	}
}

// convertDetailToVSwitchInstance 转换详情为通用子网实例
func (a *VSwitchAdapter) convertDetailToVSwitchInstance(s model.Subnet, region string) types.VSwitchInstance {
	return a.convertToVSwitchInstance(s, region)
}
