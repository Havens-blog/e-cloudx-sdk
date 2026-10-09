package aws

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
)

const (
	awsTagMaxRetries    = 3
	awsTagBaseBackoffMs = 200
)

// TagAdapterImpl AWS 标签适配器
type TagAdapterImpl struct {
	accessKeyID     string
	accessKeySecret string
	defaultRegion   string
	logger          *elog.Component
}

// NewTagAdapter 创建 AWS 标签适配器
func NewTagAdapter(accessKeyID, accessKeySecret, defaultRegion string, logger *elog.Component) *TagAdapterImpl {
	return &TagAdapterImpl{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		defaultRegion:   defaultRegion,
		logger:          logger,
	}
}

func (a *TagAdapterImpl) resolveRegion(region string) string {
	if region == "" {
		return a.defaultRegion
	}
	return region
}

// createClient 创建 EC2 客户端
func (a *TagAdapterImpl) createClient(ctx context.Context, region string) (*ec2.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(a.resolveRegion(region)),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("aws: load config failed: %w", err)
	}
	return ec2.NewFromConfig(cfg), nil
}

// retryWithBackoff 指数退避重试
func (a *TagAdapterImpl) retryWithBackoff(ctx context.Context, operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= awsTagMaxRetries; attempt++ {
		lastErr = operation()
		if lastErr == nil {
			return nil
		}
		if attempt < awsTagMaxRetries {
			backoff := time.Duration(float64(awsTagBaseBackoffMs)*math.Pow(2, float64(attempt))) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return fmt.Errorf("aws: tag API failed after %d retries: %w", awsTagMaxRetries, lastErr)
}

// ListTagKeys 查询标签键列表
func (a *TagAdapterImpl) ListTagKeys(ctx context.Context, region string) ([]string, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	var keys []string
	err = a.retryWithBackoff(ctx, func() error {
		output, e := client.DescribeTags(ctx, &ec2.DescribeTagsInput{
			MaxResults: awssdk.Int32(1000),
		})
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, td := range output.Tags {
			k := awssdk.ToString(td.Key)
			if !seen[k] {
				keys = append(keys, k)
				seen[k] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// ListTagValues 查询指定标签键的值列表
func (a *TagAdapterImpl) ListTagValues(ctx context.Context, region, key string) ([]string, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	var values []string
	err = a.retryWithBackoff(ctx, func() error {
		output, e := client.DescribeTags(ctx, &ec2.DescribeTagsInput{
			Filters: []ec2types.Filter{
				{Name: awssdk.String("key"), Values: []string{key}},
			},
			MaxResults: awssdk.Int32(1000),
		})
		if e != nil {
			return e
		}

		seen := make(map[string]bool)
		for _, td := range output.Tags {
			v := awssdk.ToString(td.Value)
			if !seen[v] {
				values = append(values, v)
				seen[v] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}

// GetResourceTags 查询资源绑定的标签
func (a *TagAdapterImpl) GetResourceTags(ctx context.Context, region, resourceType, resourceID string) (map[string]string, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	tags := make(map[string]string)
	err = a.retryWithBackoff(ctx, func() error {
		output, e := client.DescribeTags(ctx, &ec2.DescribeTagsInput{
			Filters: []ec2types.Filter{
				{Name: awssdk.String("resource-id"), Values: []string{resourceID}},
			},
		})
		if e != nil {
			return e
		}

		for _, td := range output.Tags {
			tags[awssdk.ToString(td.Key)] = awssdk.ToString(td.Value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// TagResource 为资源绑定标签
func (a *TagAdapterImpl) TagResource(ctx context.Context, region, resourceType, resourceID string, tags map[string]string) error {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return err
	}

	ec2Tags := make([]ec2types.Tag, 0, len(tags))
	for k, v := range tags {
		ec2Tags = append(ec2Tags, ec2types.Tag{
			Key:   awssdk.String(k),
			Value: awssdk.String(v),
		})
	}

	return a.retryWithBackoff(ctx, func() error {
		_, e := client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{resourceID},
			Tags:      ec2Tags,
		})
		return e
	})
}

// UntagResource 解绑资源标签
func (a *TagAdapterImpl) UntagResource(ctx context.Context, region, resourceType, resourceID string, tagKeys []string) error {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return err
	}

	ec2Tags := make([]ec2types.Tag, 0, len(tagKeys))
	for _, k := range tagKeys {
		ec2Tags = append(ec2Tags, ec2types.Tag{Key: awssdk.String(k)})
	}

	return a.retryWithBackoff(ctx, func() error {
		_, e := client.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{resourceID},
			Tags:      ec2Tags,
		})
		return e
	})
}

// ListResourcesByTag 按标签查询资源列表
func (a *TagAdapterImpl) ListResourcesByTag(ctx context.Context, region, key, value string) ([]cloudx.TaggedResource, error) {
	client, err := a.createClient(ctx, region)
	if err != nil {
		return nil, err
	}

	resolvedRegion := a.resolveRegion(region)
	var resources []cloudx.TaggedResource
	err = a.retryWithBackoff(ctx, func() error {
		output, e := client.DescribeTags(ctx, &ec2.DescribeTagsInput{
			Filters: []ec2types.Filter{
				{Name: awssdk.String("key"), Values: []string{key}},
				{Name: awssdk.String("value"), Values: []string{value}},
			},
			MaxResults: awssdk.Int32(1000),
		})
		if e != nil {
			return e
		}

		for _, td := range output.Tags {
			resources = append(resources, cloudx.TaggedResource{
				ResourceType: string(td.ResourceType),
				ResourceID:   awssdk.ToString(td.ResourceId),
				Region:       resolvedRegion,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resources, nil
}

// Ensure compile-time interface compliance
var _ cloudx.TagAdapter = (*TagAdapterImpl)(nil)
