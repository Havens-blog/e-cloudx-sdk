package aws

import (
	"context"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cloudfrontTypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/gotomicro/ego/core/elog"
)

// awsARNService 从 ARN 提取服务名(arn:aws:SERVICE:region:account:resource)。
func awsARNService(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// classifyALBTargetID ALB 目标 ID 分类:instance(i-)/ip/lambda(arn:)。
func classifyALBTargetID(id string) (kind, value string) {
	switch {
	case id == "":
		return "", ""
	case strings.HasPrefix(id, "arn:"):
		return "lambda", id
	case strings.HasPrefix(id, "i-"):
		return "instance", id
	default:
		return "ip", id
	}
}

// dedupStrings 去重、剔除空串、保持首现顺序。
func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// cfResources 单个 WebACL 关联的 CloudFront 分配聚合结果。
type cfResources struct {
	hosts   []string // 防护域名:分配域名 + CNAME 别名
	sources []string // 源站:分配 Origins 回源地址(域名或 IP)
}

// loadAWSConfig 加载带凭证的 AWS 配置(各服务客户端共享)。
func (a *WAFAdapter) loadAWSConfig(ctx context.Context, region string) (awssdk.Config, error) {
	if region == "" {
		region = a.defaultRegion
	}
	return config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			a.accessKeyID, a.accessKeySecret, "",
		)),
	)
}

// resolveWAFResources 回捞 Regional WebACL 关联资源,解析为 (防护域名, 源站地址)。
// CloudFront(全局)WebACL 的关联不走 ListResourcesForWebACL(该 API 对 CloudFront
// scope 返回空),改由 buildCloudFrontIndex + WebACLId 匹配(见 listWebACLs)。
// 本函数仅处理 Regional 关联资源:elasticloadbalancing → DNSName=防护域名、目标=源站;
// 其他(API GW/AppSync/Cognito)→ ARN 兜底为防护域名。
func (a *WAFAdapter) resolveWAFResources(ctx context.Context, arn, region string) (hosts, sources []string) {
	switch awsARNService(arn) {
	case "elasticloadbalancing":
		return a.resolveALB(ctx, arn, region)
	default:
		return []string{arn}, nil
	}
}

// buildCloudFrontIndex 只读构建 CloudFront 分配索引:WebACLId(WebACL ARN)→ 分配聚合。
// AWS 侧真实关联以 CloudFront 分配的 WebACLId 字段为准;ListResourcesForWebACL
// 对 CloudFront scope 不回资源(项目实盘验证),故从分配侧反查。
// best-effort:任一错误即返回已累积的索引(调用方据此降级)。
func (a *WAFAdapter) buildCloudFrontIndex(ctx context.Context) map[string]cfResources {
	index := make(map[string]cfResources)
	client, err := a.newCloudFrontClient(ctx)
	if err != nil {
		a.logger.Warn("创建 CloudFront 客户端失败", elog.FieldErr(err))
		return index
	}
	var marker *string
	for {
		out, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{Marker: marker})
		if err != nil {
			a.logger.Warn("获取 CloudFront 分配列表失败", elog.FieldErr(err))
			return index
		}
		if out.DistributionList != nil {
			for _, d := range out.DistributionList.Items {
				if d.WebACLId == nil || awssdk.ToString(d.WebACLId) == "" {
					continue
				}
				key := awssdk.ToString(d.WebACLId)
				r := index[key]
				r.hosts = append(r.hosts, awssdk.ToString(d.DomainName))
				r.hosts = append(r.hosts, cfAliasItems(d.Aliases)...)
				r.sources = append(r.sources, cfOriginDomains(d.Origins)...)
				index[key] = r
			}
		}
		if out.DistributionList == nil || out.DistributionList.NextMarker == nil {
			break
		}
		marker = out.DistributionList.NextMarker
	}
	return index
}

// cfOriginDomains 提取分配 Origins 回源地址(域名 + OriginPath)。
func cfOriginDomains(o *cloudfrontTypes.Origins) []string {
	if o == nil {
		return nil
	}
	out := make([]string, 0, len(o.Items))
	for _, it := range o.Items {
		if it.DomainName == nil {
			continue
		}
		d := awssdk.ToString(it.DomainName)
		if it.OriginPath != nil && awssdk.ToString(it.OriginPath) != "" {
			d += awssdk.ToString(it.OriginPath)
		}
		out = append(out, d)
	}
	return out
}

// cfAliasItems 提取分配 CNAME 别名。
func cfAliasItems(a *cloudfrontTypes.Aliases) []string {
	if a == nil {
		return nil
	}
	return a.Items
}

// resolveALB 解析 ALB 的防护域名(DNSName)与源站(目标组内 IP/实例私有 IP)。
func (a *WAFAdapter) resolveALB(ctx context.Context, arn, region string) (hosts, sources []string) {
	elb, err := a.newELBClient(ctx, region)
	if err != nil {
		a.logger.Warn("创建 ELB 客户端失败", elog.FieldErr(err))
		return []string{arn}, nil
	}
	lb, err := elb.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{arn},
	})
	if err != nil || len(lb.LoadBalancers) == 0 || lb.LoadBalancers[0].DNSName == nil {
		return []string{arn}, nil
	}
	hosts = append(hosts, awssdk.ToString(lb.LoadBalancers[0].DNSName))

	tgs, err := elb.DescribeTargetGroups(ctx, &elbv2.DescribeTargetGroupsInput{
		LoadBalancerArn: awssdk.String(arn),
	})
	if err != nil {
		return dedupStrings(hosts), nil
	}
	ec2c, ec2err := a.newEC2Client(ctx, region)
	for _, tg := range tgs.TargetGroups {
		if tg.TargetGroupArn == nil {
			continue
		}
		th, err := elb.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
			TargetGroupArn: tg.TargetGroupArn,
		})
		if err != nil {
			continue
		}
		for _, t := range th.TargetHealthDescriptions {
			if t.Target == nil {
				continue
			}
			kind, val := classifyALBTargetID(awssdk.ToString(t.Target.Id))
			switch kind {
			case "ip":
				sources = append(sources, val)
			case "instance":
				if ec2err == nil {
					if ip := a.resolveInstancePrivateIP(ctx, ec2c, val); ip != "" {
						sources = append(sources, ip)
						continue
					}
				}
				sources = append(sources, val) // 私有 IP 解析失败回退实例 ID
			case "lambda", "":
				// Lambda/空 ID 无本真源站 IP,跳过
			}
		}
	}
	return dedupStrings(hosts), dedupStrings(sources)
}

// resolveInstancePrivateIP 经 EC2 实例 ID 解析私有 IP(best-effort)。
func (a *WAFAdapter) resolveInstancePrivateIP(ctx context.Context, c *ec2.Client, instanceID string) string {
	out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return ""
	}
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.PrivateIpAddress != nil {
				return awssdk.ToString(inst.PrivateIpAddress)
			}
		}
	}
	return ""
}

// newCloudFrontClient 创建 CloudFront 客户端(全局资源固定 us-east-1)。
func (a *WAFAdapter) newCloudFrontClient(ctx context.Context) (*cloudfront.Client, error) {
	cfg, err := a.loadAWSConfig(ctx, "us-east-1")
	if err != nil {
		return nil, err
	}
	return cloudfront.NewFromConfig(cfg), nil
}

// newELBClient 创建 ELB 客户端。
func (a *WAFAdapter) newELBClient(ctx context.Context, region string) (*elbv2.Client, error) {
	cfg, err := a.loadAWSConfig(ctx, region)
	if err != nil {
		return nil, err
	}
	return elbv2.NewFromConfig(cfg), nil
}

// newEC2Client 创建 EC2 客户端。
func (a *WAFAdapter) newEC2Client(ctx context.Context, region string) (*ec2.Client, error) {
	cfg, err := a.loadAWSConfig(ctx, region)
	if err != nil {
		return nil, err
	}
	return ec2.NewFromConfig(cfg), nil
}