package aws

import (
	"context"
	"fmt"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gotomicro/ego/core/elog"
)

// S3Adapter AWS S3 适配器
type S3Adapter struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component

	// S3 指标查询(CloudWatch AWS/S3)状态,见 oss_metrics.go
	// ossMetricHooks 测试注入钩子(非 nil 时替代真实 region 解析与 CloudWatch 客户端创建)
	ossMetricHooks *ossMetricHooks
}

// NewS3Adapter 创建 S3 适配器
func NewS3Adapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *S3Adapter {
	return &S3Adapter{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

// createClient 创建 S3 客户端
func (a *S3Adapter) createClient(ctx context.Context, region string) (*s3.Client, error) {
	if region == "" {
		region = a.defaultRegion
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID,
			a.accessKeySecret,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("加载AWS配置失败: %w", err)
	}

	return s3.NewFromConfig(cfg), nil
}

// ListBuckets 获取存储桶列表
func (a *S3Adapter) ListBuckets(ctx context.Context, region string) ([]types.OSSBucket, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	output, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("获取S3存储桶列表失败: %w", err)
	}

	// 列表 N+1 回填(标签)需要桶所在 region 的客户端;
	// 按 region 惰性创建并复用,避免每桶重复加载 AWS 配置放大成本。
	regionClients := map[string]*s3.Client{}
	getRegionClient := func(r string) *s3.Client {
		if c, ok := regionClients[r]; ok {
			return c
		}
		c, err := a.createClient(ctx, r)
		if err != nil {
			return nil
		}
		regionClients[r] = c
		return c
	}

	var buckets []types.OSSBucket
	for _, bucket := range output.Buckets {
		ossBucket := a.convertToOSSBucket(&bucket)

		// 获取 bucket 的 region(列表 N+1,与 GetBucket 详情同源,口径一致)
		bucketRegion, err := a.getBucketRegion(ctx, client, *bucket.Name)
		if err == nil {
			ossBucket.Region = bucketRegion
			ossBucket.Location = bucketRegion
		}

		// 如果指定了 region，只返回该 region 的 bucket
		if region != "" && ossBucket.Region != region {
			continue
		}

		// N+1 回填标签:S3 列表接口不返回标签,GetBucketTagging 必须使用
		// 桶所在 region 的端点(否则 SDK 返回 PermanentRedirect)。
		// 每桶 1 次额外 GET,请求数随桶数线性增长;无标签桶返回
		// NoSuchTagSet(404)属正常,所有失败均静默降级为空,不打断列表。
		if ossBucket.Region != "" {
			if rc := getRegionClient(ossBucket.Region); rc != nil {
				tagOutput, err := rc.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
					Bucket: aws.String(*bucket.Name),
				})
				if err == nil {
					ossBucket.Tags = a.mapS3Tags(tagOutput.TagSet)
				}
			}
		}

		// 获取 bucket 统计信息（存储大小和对象数量）
		stats, err := a.GetBucketStats(ctx, *bucket.Name)
		if err == nil && stats != nil {
			ossBucket.ObjectCount = stats.ObjectCount
			ossBucket.StorageSize = stats.StorageSize
		} else {
			a.logger.Warn("获取S3存储桶统计信息失败",
				elog.String("bucket", *bucket.Name),
				elog.FieldErr(err))
		}

		buckets = append(buckets, ossBucket)
	}

	return buckets, nil
}

// getBucketRegion 获取 bucket 所在的 region。
// 列表(ListBuckets)与详情(GetBucket)共用本函数,保证 Region/Location 口径一致。
func (a *S3Adapter) getBucketRegion(ctx context.Context, client *s3.Client, bucketName string) (string, error) {
	output, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return "", err
	}
	return a.locationConstraintToRegion(output.LocationConstraint), nil
}

// locationConstraintToRegion 将 GetBucketLocation 返回的 LocationConstraint
// 映射为 region 名。S3 对 us-east-1 桶返回空 LocationConstraint
// (响应体为空,SDK 解析为零值 "",并无 nil 指针),因此空值统一映射为 us-east-1;
// 其余值原样透传(如 ap-guangzhou、cn-north-1)。
func (a *S3Adapter) locationConstraintToRegion(lc s3types.BucketLocationConstraint) string {
	if lc == "" {
		return "us-east-1"
	}
	return string(lc)
}

// GetBucket 获取单个存储桶详情
func (a *S3Adapter) GetBucket(ctx context.Context, bucketName string) (*types.OSSBucket, error) {
	client, err := a.createClient(ctx, "")
	if err != nil {
		return nil, err
	}

	// 获取 bucket region
	bucketRegion, err := a.getBucketRegion(ctx, client, bucketName)
	if err != nil {
		return nil, fmt.Errorf("获取存储桶region失败: %w", err)
	}

	// 使用正确的 region 创建客户端
	regionClient, err := a.createClient(ctx, bucketRegion)
	if err != nil {
		return nil, err
	}

	bucket := &types.OSSBucket{
		BucketName: bucketName,
		Region:     bucketRegion,
		Location:   bucketRegion,
		Provider:   string(domain.CloudProviderAWS),
		// StorageClass 不填:S3 详情接口同样拿不到桶级存储类型,
		// 与列表口径一致(取舍理由见 convertToOSSBucket 注释)。
	}

	// 获取 ACL
	aclOutput, err := regionClient.GetBucketAcl(ctx, &s3.GetBucketAclInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		bucket.ACL = a.parseACL(aclOutput)
	}

	// 获取版本控制状态
	versionOutput, err := regionClient.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		bucket.Versioning = string(versionOutput.Status)
	}

	// 获取加密配置
	encryptOutput, err := regionClient.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil && encryptOutput.ServerSideEncryptionConfiguration != nil {
		for _, rule := range encryptOutput.ServerSideEncryptionConfiguration.Rules {
			if rule.ApplyServerSideEncryptionByDefault != nil {
				bucket.ServerSideEncryption = string(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm)
				if rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID != nil {
					bucket.KMSKeyID = *rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID
				}
			}
		}
	}

	// 获取桶标签(与列表口径一致:失败或无标签桶返回 NoSuchTagSet
	// 均静默留空,不视为错误)
	tagOutput, err := regionClient.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		bucket.Tags = a.mapS3Tags(tagOutput.TagSet)
	}

	// 设置 endpoint
	bucket.ExtranetEndpoint = fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, bucketRegion)
	bucket.IntranetEndpoint = fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, bucketRegion)

	return bucket, nil
}

// parseACL 解析 ACL
func (a *S3Adapter) parseACL(output *s3.GetBucketAclOutput) string {
	if output == nil {
		return "private"
	}

	hasPublicRead := false
	hasPublicWrite := false

	for _, grant := range output.Grants {
		if grant.Grantee != nil && grant.Grantee.URI != nil {
			uri := *grant.Grantee.URI
			if uri == "http://acs.amazonaws.com/groups/global/AllUsers" {
				if grant.Permission == s3types.PermissionRead {
					hasPublicRead = true
				}
				if grant.Permission == s3types.PermissionWrite {
					hasPublicWrite = true
				}
			}
		}
	}

	if hasPublicRead && hasPublicWrite {
		return "public-read-write"
	}
	if hasPublicRead {
		return "public-read"
	}
	return "private"
}

// GetBucketStats 获取存储桶统计信息
func (a *S3Adapter) GetBucketStats(ctx context.Context, bucketName string) (*types.OSSBucketStats, error) {
	// S3 没有直接的 API 获取统计信息，需要通过 CloudWatch 或遍历对象
	// 这里返回基本信息
	return &types.OSSBucketStats{
		BucketName: bucketName,
	}, nil
}

// ListBucketsWithFilter 带过滤条件获取存储桶列表
func (a *S3Adapter) ListBucketsWithFilter(ctx context.Context, region string, filter *types.OSSBucketFilter) ([]types.OSSBucket, error) {
	buckets, err := a.ListBuckets(ctx, region)
	if err != nil {
		return nil, err
	}

	if filter == nil {
		return buckets, nil
	}

	var filtered []types.OSSBucket
	for _, bucket := range buckets {
		// 按前缀过滤
		if filter.Prefix != "" && len(bucket.BucketName) >= len(filter.Prefix) {
			if bucket.BucketName[:len(filter.Prefix)] != filter.Prefix {
				continue
			}
		}
		// 按存储类型过滤
		if filter.StorageClass != "" && bucket.StorageClass != filter.StorageClass {
			continue
		}
		filtered = append(filtered, bucket)
	}

	return filtered, nil
}

// convertToOSSBucket 转换为统一的 OSS 存储桶结构。
// 注意:Region/Location 不在此处填写,由 ListBuckets/GetBucket 的
// GetBucketLocation N+1 回填(见 getBucketRegion),避免转换函数伪造地域。
func (a *S3Adapter) convertToOSSBucket(bucket *s3types.Bucket) types.OSSBucket {
	ossBucket := types.OSSBucket{
		Provider: string(domain.CloudProviderAWS),
		// StorageClass 不填:S3 列表与详情接口(GetBucketLocation/GetBucketTagging
		// 等)均不提供桶级存储类型;若强行取值需 ListObjectsV2 取首个对象的
		// StorageClass,那是对象级类型、不代表桶级事实,且为逐桶额外请求,
		// 成本高,故放弃回填。硬编码 "STANDARD" 是错误值,删去后置空,
		// 由前端显示 '-'。
	}

	if bucket.Name != nil {
		ossBucket.BucketName = *bucket.Name
	}
	if bucket.CreationDate != nil {
		ossBucket.CreationTime = *bucket.CreationDate
	}

	return ossBucket
}

// mapS3Tags 将 S3 GetBucketTagging 返回的 TagSet 规整为统一的标签 map。
// 列表与详情共用,保证口径一致;空 Key 或无标签时返回 nil(即不填)。
func (a *S3Adapter) mapS3Tags(tagSet []s3types.Tag) map[string]string {
	if len(tagSet) == 0 {
		return nil
	}
	tags := make(map[string]string, len(tagSet))
	for _, t := range tagSet {
		if t.Key == nil || *t.Key == "" {
			continue
		}
		tags[*t.Key] = aws.ToString(t.Value)
	}
	return tags
}
