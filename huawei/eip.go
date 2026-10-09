package huawei

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	eipv3 "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v3"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v3/model"
	eipregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v3/region"
)

// EIPAdapter 华为云EIP适配器（使用 v3 API，支持 ELB 绑定信息）
type EIPAdapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewEIPAdapter 创建EIP适配器
func NewEIPAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *EIPAdapter {
	return &EIPAdapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 EIP v3 客户端
func (a *EIPAdapter) createClient(region string) (*eipv3.EipClient, error) {
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

	r, err := eipregion.SafeValueOf(region)
	if err != nil {
		return nil, fmt.Errorf("无效的地域: %s", region)
	}

	client, err := eipv3.EipClientBuilder().
		WithRegion(r).
		WithCredential(auth).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("创建华为云EIP客户端失败: %w", err)
	}

	return eipv3.NewEipClient(client), nil
}

// ListInstances 获取EIP列表
func (a *EIPAdapter) ListInstances(ctx context.Context, region string) ([]types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	var allEIPs []types.EIPInstance
	var marker *string
	limit := int32(100)

	for {
		request := &model.ListPublicipsRequest{
			Limit:  &limit,
			Marker: marker,
		}

		response, err := client.ListPublicips(request)
		if err != nil {
			return nil, fmt.Errorf("获取EIP列表失败: %w", err)
		}

		if response.Publicips == nil || len(*response.Publicips) == 0 {
			break
		}

		for _, e := range *response.Publicips {
			allEIPs = append(allEIPs, a.convertToEIPInstance(e, region))
		}

		if len(*response.Publicips) < int(limit) {
			break
		}
		lastEIP := (*response.Publicips)[len(*response.Publicips)-1]
		marker = lastEIP.Id
	}

	a.logger.Info("获取华为云EIP列表成功",
		elog.String("region", region),
		elog.Int("count", len(allEIPs)))

	return allEIPs, nil
}

// GetInstance 获取单个EIP详情
func (a *EIPAdapter) GetInstance(ctx context.Context, region, allocationID string) (*types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ShowPublicipRequest{
		PublicipId: allocationID,
	}

	response, err := client.ShowPublicip(request)
	if err != nil {
		return nil, fmt.Errorf("获取EIP详情失败: %w", err)
	}

	if response.Publicip == nil {
		return nil, fmt.Errorf("EIP不存在: %s", allocationID)
	}

	instance := a.convertToEIPInstance(*response.Publicip, region)
	return &instance, nil
}

// ListInstancesByIDs 批量获取EIP
func (a *EIPAdapter) ListInstancesByIDs(ctx context.Context, region string, allocationIDs []string) ([]types.EIPInstance, error) {
	if len(allocationIDs) == 0 {
		return nil, nil
	}

	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ListPublicipsRequest{
		Id: &allocationIDs,
	}

	response, err := client.ListPublicips(request)
	if err != nil {
		return nil, fmt.Errorf("批量获取EIP失败: %w", err)
	}

	var result []types.EIPInstance
	if response.Publicips != nil {
		for _, e := range *response.Publicips {
			result = append(result, a.convertToEIPInstance(e, region))
		}
	}

	return result, nil
}

// GetInstanceStatus 获取EIP状态
func (a *EIPAdapter) GetInstanceStatus(ctx context.Context, region, allocationID string) (string, error) {
	instance, err := a.GetInstance(ctx, region, allocationID)
	if err != nil {
		return "", err
	}
	return instance.Status, nil
}

// ListInstancesWithFilter 带过滤条件获取EIP列表
func (a *EIPAdapter) ListInstancesWithFilter(ctx context.Context, region string, filter *types.EIPInstanceFilter) ([]types.EIPInstance, error) {
	client, err := a.createClient(region)
	if err != nil {
		return nil, err
	}

	request := &model.ListPublicipsRequest{}

	if filter != nil {
		if len(filter.AllocationIDs) > 0 {
			request.Id = &filter.AllocationIDs
		}
		if len(filter.IPAddresses) > 0 {
			request.PublicIpAddress = &filter.IPAddresses
		}
		if filter.PageSize > 0 {
			limit := int32(filter.PageSize)
			request.Limit = &limit
		}
	}

	response, err := client.ListPublicips(request)
	if err != nil {
		return nil, fmt.Errorf("获取EIP列表失败: %w", err)
	}

	var result []types.EIPInstance
	if response.Publicips != nil {
		for _, e := range *response.Publicips {
			result = append(result, a.convertToEIPInstance(e, region))
		}
	}

	return result, nil
}

// normalizeAssociateType 将华为云 AssociateInstanceType 转换为统一的实例类型
func normalizeAssociateType(assocType string) string {
	switch assocType {
	case "PORT":
		// PORT 类型需要进一步通过 Vnic.DeviceOwner 判断
		return ""
	case "ELB", "ELBV1":
		return "SlbInstance"
	case "NATGW":
		return "Nat"
	case "VPN":
		return "VpnGateway"
	default:
		return assocType
	}
}

// normalizeDeviceOwner 将华为云 DeviceOwner 转换为统一的实例类型
func normalizeDeviceOwner(deviceOwner string) string {
	switch deviceOwner {
	case "compute:nova":
		return "EcsInstance"
	case "neutron:LOADBALANCERV2", "neutron:LOADBALANCER":
		return "SlbInstance"
	case "neutron:VIP_PORT":
		return "HaVip"
	case "network:nat_gateway":
		return "Nat"
	case "network:VPN":
		return "VpnGateway"
	case "network:router_interface", "network:router_interface_distributed":
		return "RouterInterface"
	default:
		// compute:cn-south-1f 这种格式也是 ECS
		if len(deviceOwner) > 8 && deviceOwner[:8] == "compute:" {
			return "EcsInstance"
		}
		return deviceOwner
	}
}

// convertToEIPInstance 转换为通用EIP实例
func (a *EIPAdapter) convertToEIPInstance(e model.PublicipSingleShowResp, region string) types.EIPInstance {
	allocationID := ""
	if e.Id != nil {
		allocationID = *e.Id
	}

	publicIP := ""
	if e.PublicIpAddress != nil {
		publicIP = *e.PublicIpAddress
	}

	name := ""
	if e.Alias != nil {
		name = *e.Alias
	}

	description := ""
	if e.Description != nil {
		description = *e.Description
	}

	// 解析绑定信息：优先用 AssociateInstanceType/Id，再用 Vnic 补充
	instanceID := ""
	instanceType := ""
	status := "Available"
	portID := ""
	privateIP := ""
	vpcID := ""

	// 1. 从 AssociateInstanceType/Id 获取绑定信息（ELB/NATGW/VPN 直接有值）
	if e.AssociateInstanceId != nil && *e.AssociateInstanceId != "" {
		instanceID = *e.AssociateInstanceId
		status = "InUse"
	}
	if e.AssociateInstanceType != nil {
		assocType := e.AssociateInstanceType.Value()
		normalized := normalizeAssociateType(assocType)
		if normalized != "" {
			instanceType = normalized
		}
	}

	// 2. 从 Vnic 获取更详细的绑定信息（PORT 类型的 EIP 通过 Vnic 关联到 ECS/RDS 等）
	if e.Vnic != nil {
		if e.Vnic.PortId != nil {
			portID = *e.Vnic.PortId
		}
		if e.Vnic.PrivateIpAddress != nil {
			privateIP = *e.Vnic.PrivateIpAddress
		}
		if e.Vnic.VpcId != nil {
			vpcID = *e.Vnic.VpcId
		}
		// 如果 AssociateInstanceType 是 PORT，用 DeviceOwner 推断真实类型
		if instanceType == "" && e.Vnic.DeviceOwner != nil {
			instanceType = normalizeDeviceOwner(*e.Vnic.DeviceOwner)
		}
		// 如果 AssociateInstanceId 是 Port ID，用 DeviceId 作为真实实例 ID
		if e.Vnic.DeviceId != nil && *e.Vnic.DeviceId != "" {
			instanceID = *e.Vnic.DeviceId
			status = "InUse"
		}
		// Vnic.InstanceId 可能有更精确的实例 ID（如 RDS 实例 ID）
		if e.Vnic.InstanceId != nil && *e.Vnic.InstanceId != "" {
			instanceID = *e.Vnic.InstanceId
		}
	}

	// 3. 用 Status 补充状态
	if e.Status != nil {
		rawStatus := e.Status.Value()
		switch rawStatus {
		case "ACTIVE", "ELB", "VPN":
			status = "InUse"
		case "DOWN":
			if portID == "" {
				status = "Available"
			}
		case "BIND_ERROR":
			status = "Error"
		case "FREEZED":
			status = "Frozen"
		}
	}

	// 带宽信息
	bandwidth := 0
	chargeType := ""
	bandwidthID := ""
	if e.Bandwidth != nil {
		if e.Bandwidth.Size != nil {
			bandwidth = int(*e.Bandwidth.Size)
		}
		if e.Bandwidth.ShareType != nil {
			chargeType = *e.Bandwidth.ShareType
		}
		if e.Bandwidth.Id != nil {
			bandwidthID = *e.Bandwidth.Id
		}
	}

	createTime := ""
	if e.CreatedAt != nil {
		createTime = e.CreatedAt.String()
	}

	ipVersion := "IPv4"
	if e.IpVersion != nil {
		if e.IpVersion.Value() == 6 {
			ipVersion = "IPv6"
		}
	}

	return types.EIPInstance{
		AllocationID:       allocationID,
		IPAddress:          publicIP,
		Name:               name,
		Status:             types.NormalizeEIPStatus("huawei", status),
		Region:             region,
		Description:        description,
		Bandwidth:          bandwidth,
		InternetChargeType: chargeType,
		BandwidthPackageID: bandwidthID,
		IPVersion:          ipVersion,
		InstanceID:         instanceID,
		InstanceType:       instanceType,
		PrivateIPAddress:   privateIP,
		VPCID:              vpcID,
		NetworkInterface:   portID,
		CreationTime:       createTime,
		Tags:               make(map[string]string),
		Provider:           "huawei",
	}
}
