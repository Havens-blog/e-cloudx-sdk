package aliyun

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

func init() {
	// 注册阿里云适配器创建函数
	cloudx.RegisterAdapter(domain.CloudProviderAliyun, func(account *domain.CloudAccount) (cloudx.CloudAdapter, error) {
		return NewAdapter(account)
	})
}

// Adapter 阿里云统一适配器
type Adapter struct {
	account       *domain.CloudAccount
	logger        *elog.Component
	asset         *AssetAdapter
	ecs           *ECSAdapter
	securityGroup *SecurityGroupAdapter
	image         *ImageAdapter
	disk          *DiskAdapter
	snapshot      *SnapshotAdapter
	rds           *RDSAdapter
	redis         *RedisAdapter
	mongodb       *MongoDBAdapter
	vpc           *VPCAdapter
	eip           *EIPAdapter
	eni           *ENIAdapter
	lb            *LBAdapter
	cdn           *CDNAdapter
	waf           *WAFAdapter
	ddos          cloudx.DDOSAdapter // DDoS防护(高防/原生防护);实现见 ddos.go
	nas           *NASAdapter
	oss           *OSSAdapter
	kafka         *KafkaAdapter
	elasticsearch *ElasticsearchAdapter
	iam           *IAMAdapter
	vswitch       *VSwitchAdapter
	dns           *DNSAdapter
	tag           *TagAdapterImpl
	ecsCreate     *ECSCreateAdapterImpl
	resourceQuery *ResourceQueryAdapterImpl
}

// NewAdapter 创建阿里云适配器
func NewAdapter(account *domain.CloudAccount) (*Adapter, error) {
	if account == nil {
		return nil, cloudx.ErrInvalidConfig
	}

	logger := elog.DefaultLogger
	if logger == nil {
		logger = elog.EgoLogger
	}

	// 获取默认地域
	defaultRegion := "cn-hangzhou"
	if len(account.Regions) > 0 {
		defaultRegion = account.Regions[0]
	}

	adapter := &Adapter{
		account: account,
		logger:  logger,
	}

	// 创建资产适配器 (已废弃，保留兼容)
	adapter.asset = NewAssetAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建ECS适配器 (推荐使用)
	adapter.ecs = NewECSAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建安全组适配器
	adapter.securityGroup = NewSecurityGroupAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建镜像适配器
	adapter.image = NewImageAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建云盘适配器
	adapter.disk = NewDiskAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建快照适配器
	adapter.snapshot = NewSnapshotAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建RDS适配器
	adapter.rds = NewRDSAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建Redis适配器
	adapter.redis = NewRedisAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建MongoDB适配器
	adapter.mongodb = NewMongoDBAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建VPC适配器
	adapter.vpc = NewVPCAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建EIP适配器
	adapter.eip = NewEIPAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建ENI适配器
	adapter.eni = NewENIAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建LB适配器
	adapter.lb = NewLBAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建CDN适配器
	adapter.cdn = NewCDNAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建WAF适配器
	adapter.waf = NewWAFAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建DDoS防护适配器(高防IP ddoscoo + DDoS原生防护 ddosbgp)
	adapter.ddos = NewDDOSAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建NAS适配器
	adapter.nas = NewNASAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建OSS适配器
	adapter.oss = NewOSSAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建Kafka适配器
	adapter.kafka = NewKafkaAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建Elasticsearch适配器
	adapter.elasticsearch = NewElasticsearchAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建IAM适配器
	adapter.iam = NewIAMAdapter(account, logger)

	// 创建VSwitch适配器
	adapter.vswitch = NewVSwitchAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建标签适配器
	adapter.tag = NewTagAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建ECS创建适配器
	adapter.ecsCreate = NewECSCreateAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建资源查询适配器
	adapter.resourceQuery = NewResourceQueryAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	// 创建DNS适配器
	adapter.dns = NewDNSAdapter(
		account.AccessKeyID,
		account.AccessKeySecret,
		defaultRegion,
		logger,
	)

	return adapter, nil
}

// GetProvider 获取云厂商类型
func (a *Adapter) GetProvider() domain.CloudProvider {
	return domain.CloudProviderAliyun
}

// Asset 获取资产适配器
// Deprecated: 请使用 ECS() 获取云虚拟机适配器
func (a *Adapter) Asset() cloudx.AssetAdapter {
	return a.asset
}

// ECS 获取ECS适配器
func (a *Adapter) ECS() cloudx.ECSAdapter {
	return a.ecs
}

// SecurityGroup 获取安全组适配器
func (a *Adapter) SecurityGroup() cloudx.SecurityGroupAdapter {
	return a.securityGroup
}

// Image 获取镜像适配器
func (a *Adapter) Image() cloudx.ImageAdapter {
	return a.image
}

// Disk 获取云盘适配器
func (a *Adapter) Disk() cloudx.DiskAdapter {
	return a.disk
}

// Snapshot 获取快照适配器
func (a *Adapter) Snapshot() cloudx.SnapshotAdapter {
	return a.snapshot
}

// RDS 获取RDS适配器
func (a *Adapter) RDS() cloudx.RDSAdapter {
	return a.rds
}

// Redis 获取Redis适配器
func (a *Adapter) Redis() cloudx.RedisAdapter {
	return a.redis
}

// MongoDB 获取MongoDB适配器
func (a *Adapter) MongoDB() cloudx.MongoDBAdapter {
	return a.mongodb
}

// VPC 获取VPC适配器
func (a *Adapter) VPC() cloudx.VPCAdapter {
	return a.vpc
}

// EIP 获取EIP适配器
func (a *Adapter) EIP() cloudx.EIPAdapter {
	return a.eip
}

// ENI 获取弹性网卡适配器
func (a *Adapter) ENI() cloudx.ENIAdapter {
	return a.eni
}

// LB 获取负载均衡适配器
func (a *Adapter) LB() cloudx.LBAdapter {
	return a.lb
}

// CDN 获取CDN适配器
func (a *Adapter) CDN() cloudx.CDNAdapter {
	return a.cdn
}

// WAF 获取WAF适配器
func (a *Adapter) WAF() cloudx.WAFAdapter {
	return a.waf
}

// DDoS 获取DDoS防护适配器
func (a *Adapter) DDOS() cloudx.DDOSAdapter {
	return a.ddos
}

// DNS 获取DNS适配器
func (a *Adapter) DNS() cloudx.DNSAdapter {
	return a.dns
}

// NAS 获取NAS适配器
func (a *Adapter) NAS() cloudx.NASAdapter {
	return a.nas
}

// OSS 获取OSS适配器
func (a *Adapter) OSS() cloudx.OSSAdapter {
	return a.oss
}

// Kafka 获取Kafka适配器
func (a *Adapter) Kafka() cloudx.KafkaAdapter {
	return a.kafka
}

// Elasticsearch 获取Elasticsearch适配器
func (a *Adapter) Elasticsearch() cloudx.ElasticsearchAdapter {
	return a.elasticsearch
}

// IAM 获取IAM适配器
func (a *Adapter) IAM() cloudx.IAMAdapter {
	return a.iam
}

// VSwitch 获取交换机适配器
func (a *Adapter) VSwitch() cloudx.VSwitchAdapter {
	return a.vswitch
}

// ECSCreate 获取 ECS 创建适配器
func (a *Adapter) ECSCreate() cloudx.ECSCreateAdapter {
	return a.ecsCreate
}

// ResourceQuery 获取资源查询适配器
func (a *Adapter) ResourceQuery() cloudx.ResourceQueryAdapter {
	return a.resourceQuery
}

// Tag 获取标签适配器
func (a *Adapter) Tag() cloudx.TagAdapter {
	return a.tag
}

// ValidateCredentials 验证凭证
func (a *Adapter) ValidateCredentials(ctx context.Context) error {
	// 使用ECS适配器验证凭证（获取地域列表）
	_, err := a.ecs.GetRegions(ctx)
	if err != nil {
		return fmt.Errorf("阿里云凭证验证失败: %w", err)
	}

	a.logger.Info("阿里云凭证验证成功",
		elog.Int64("account_id", a.account.ID),
		elog.String("account_name", a.account.Name))

	return nil
}
