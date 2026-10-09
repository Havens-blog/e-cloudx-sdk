package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	vpc "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/model"
	vpcregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/region"
)

// VPCAdapter 华为云VPC适配器
type VPCAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewVPCAdapter 创建VPC适配器
func NewVPCAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *VPCAdapter {
	return &VPCAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建VPC客户端
func (a *VPCAdapter) createClient(region string) (*vpc.VpcClient, error) {
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

	client, err := vpc.VpcClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云VPC客户端失败: %w", err)
	}

	return vpc.NewVpcClient(client), nil
}

// ListInstances 获取VPC列表
func (a *VPCAdapter) ListInstances(ctx context.Context, region string) ([]types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allVPCs []types.VPCInstance
	var marker *string
	limit := int32(100)

	for {
		request := &model.ListVpcsRequest{
			Limit:  &limit,
			Marker: marker,
		}

		response, err := client.ListVpcs(request)
		if err != nil {
			return nil, fmt.Errorf("获取VPC列表失败: %w", err)
		}

		if response.Vpcs == nil {
			break
		}

		for _, v := range *response.Vpcs {
			allVPCs = append(allVPCs, a.convertToVPCInstance(v, region))
		}

		if len(*response.Vpcs) < int(limit) {
			break
		}
		lastVPC := (*response.Vpcs)[len(*response.Vpcs)-1]
		marker = &lastVPC.Id
	}

	a.logger.Info("获取华为云VPC列表成功",
		elog.String("region", region),
		elog.Int("count", len(allVPCs)))

	return allVPCs, nil
}

// GetInstance 获取单个VPC详情
func (a *VPCAdapter) GetInstance(ctx context.Context, region, vpcID string) (*types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ShowVpcRequest{
		VpcId: vpcID,
	}

	response, err := client.ShowVpc(request)
	if err != nil {
		return nil, fmt.Errorf("获取VPC详情失败: %w", err)
	}

	if response.Vpc == nil {
		return nil, fmt.Errorf("VPC不存在: %s", vpcID)
	}

	instance := a.convertToVPCInstance(*response.Vpc, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取VPC
func (a *VPCAdapter) ListInstancesByIDs(ctx context.Context, region string, vpcIDs []string) ([]types.VPCInstance, error) {
	var result []types.VPCInstance
	for _, vpcID := range vpcIDs {
		instance, err := a.GetInstance(ctx, region, vpcID)
		if err != nil {
			a.logger.Warn("获取VPC失败", elog.String("vpc_id", vpcID), elog.FieldErr(err))
			continue
		}
		result = append(result, *instance)
	}
	return result, nil
}

// GetInstanceStatus 获取VPC状态
func (a *VPCAdapter) GetInstanceStatus(ctx context.Context, region, vpcID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, vpcID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取VPC列表
func (a *VPCAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.VPCInstanceFilter) ([]types.VPCInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ListVpcsRequest{}

	if filter != nil {
		if len(filter.VPCIDs) > 0 && len(filter.VPCIDs) == 1 {
			request.Id = &filter.VPCIDs[0]
		}
		if filter.PageSize > 0 {
			limit := int32(filter.PageSize)
			request.Limit = &limit
		}
	}

	response, err := client.ListVpcs(request)
	if err != nil {
		return nil, fmt.Errorf("获取VPC列表失败: %w", err)
	}

	var result []types.VPCInstance
	if response.Vpcs != nil {
		for _, v := range *response.Vpcs {
			result = append(result, a.convertToVPCInstance(v, region))
		}
	}

	return result, nil
}

// convertToVPCInstance 转换为通用VPC实例
func (a *VPCAdapter) convertToVPCInstance(v model.Vpc, region string) types.VPCInstance {
	// 提取标签
	tags := make(map[string]string)

	status := "Available"
	if v.Status.Value() != "" {
		status = v.Status.Value()
	}

	return types.VPCInstance{
		VPCID:          v.Id,
		VPCName:        v.Name,
		Status:         types.NormalizeVPCStatus("huawei", status),
		Region:         region,
		Description:    v.Description,
		CidrBlock:      v.Cidr,
		SecondaryCidrs: nil,
		ProjectID:      v.EnterpriseProjectId,
		Tags:           tags,
		Provider:       "huawei",
	}
}
