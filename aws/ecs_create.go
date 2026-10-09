package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

// ECSCreateAdapterImpl AWS EC2 实例创建适配器
type ECSCreateAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewECSCreateAdapter 创建 AWS EC2 创建适配器
func NewECSCreateAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *ECSCreateAdapterImpl {
	return &ECSCreateAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *ECSCreateAdapterImpl) getClient(region string) *ec2.Client {
	if region == "" {
		region = a.defaultRegion
	}
	return ec2.New(ec2.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(a.accessKeyID, a.accessKeySecret, ""),
	})
}

// CreateInstances 调用 AWS RunInstances API 创建实例
func (a *ECSCreateAdapterImpl) CreateInstances(ctx context.Context, params types.CreateInstanceParams) (*types.CreateInstanceResult, error) {
	client := a.getClient(params.Region)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(params.ImageID),
		InstanceType: ec2types.InstanceType(params.InstanceType),
		MinCount:     aws.Int32(int32(params.Count)),
		MaxCount:     aws.Int32(int32(params.Count)),
		SubnetId:     aws.String(params.SubnetID),
	}

	// 安全组
	if len(params.SecurityGroupIDs) > 0 {
		input.SecurityGroupIds = params.SecurityGroupIDs
	}

	// 密钥对
	if params.KeyPairName != "" {
		input.KeyName = aws.String(params.KeyPairName)
	}

	// 系统盘 + 数据盘 (BlockDeviceMappings)
	var bdms []ec2types.BlockDeviceMapping
	if params.SystemDiskType != "" || params.SystemDiskSize > 0 {
		rootBdm := ec2types.BlockDeviceMapping{
			DeviceName: aws.String("/dev/xvda"),
			Ebs:        &ec2types.EbsBlockDevice{},
		}
		if params.SystemDiskType != "" {
			rootBdm.Ebs.VolumeType = ec2types.VolumeType(params.SystemDiskType)
		}
		if params.SystemDiskSize > 0 {
			rootBdm.Ebs.VolumeSize = aws.Int32(int32(params.SystemDiskSize))
		}
		bdms = append(bdms, rootBdm)
	}
	for i, d := range params.DataDisks {
		devName := fmt.Sprintf("/dev/xvd%c", 'b'+i)
		dataBdm := ec2types.BlockDeviceMapping{
			DeviceName: aws.String(devName),
			Ebs: &ec2types.EbsBlockDevice{
				VolumeSize: aws.Int32(int32(d.Size)),
			},
		}
		if d.Category != "" {
			dataBdm.Ebs.VolumeType = ec2types.VolumeType(d.Category)
		}
		bdms = append(bdms, dataBdm)
	}
	if len(bdms) > 0 {
		input.BlockDeviceMappings = bdms
	}

	// 标签
	if len(params.Tags) > 0 || params.InstanceName != "" {
		tags := make([]ec2types.Tag, 0, len(params.Tags)+1)
		if params.InstanceName != "" {
			tags = append(tags, ec2types.Tag{Key: aws.String("Name"), Value: aws.String(params.InstanceName)})
		}
		for k, v := range params.Tags {
			tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		input.TagSpecifications = []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
		}
	}

	output, err := client.RunInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("AWS创建实例失败: %w", err)
	}

	var instanceIDs []string
	for _, inst := range output.Instances {
		if inst.InstanceId != nil {
			instanceIDs = append(instanceIDs, *inst.InstanceId)
		}
	}

	a.logger.Info("AWS创建实例成功",
		elog.Int("count", len(instanceIDs)),
		elog.String("region", params.Region))

	return &types.CreateInstanceResult{InstanceIDs: instanceIDs}, nil
}
