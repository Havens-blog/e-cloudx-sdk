// cert_test.go AWS 完整证书适配器（CertAdapter）单元测试：fake ACM/CloudFront/
// ELBv2 SDK 覆盖上传（us-east-1 地域硬约束锁定）、三产品绑定、孤儿清理（托管
// 证书显式识别拒绝）与私钥明文用后归零（cert-multicloud-deployers 任务 2）。
package aws

import (
	"context"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 测试公共设施 ====================

// newTestCertAdapter 构造可注入 fake SDK 客户端的完整证书适配器（限流器放宽避免测试排队）
func newTestCertAdapter(t *testing.T) *CertAdapter {
	t.Helper()
	adapter := NewCertAdapter(elog.DefaultLogger)
	adapter.rateLimiter = ratelimit.NewRateLimiter(5000)
	return adapter
}

// certDeployRegionCreds 主地域为 eu-west-1 的凭证（区分 CDN 固定地域与账号主地域路由）
func certDeployRegionCreds() *domain.CloudAccount {
	creds := certTestCreds()
	creds.Regions = []string{"eu-west-1"}
	return creds
}

// certDeployTestARN 构造指定地域的 ACM 证书 ARN
func certDeployTestARN(region string) string {
	return "arn:aws:acm:" + region + ":123456789012:certificate/c1"
}

// ==================== fake SDK 客户端（部署面窄接口） ====================

// fakeACMDeployClient ACM 部署面 fake：记录上传/描述/删除请求。importCalls 持有
// 构参指针（调用返回后由适配层归零，可断言用后 Zeroize）；importKeys 为调用
// 时点私钥快照（断言明文经内存透传完整性）。
type fakeACMDeployClient struct {
	importCalls  []*acm.ImportCertificateInput
	importKeys   [][]byte
	importOut    *acm.ImportCertificateOutput
	importErr    error
	describeArns []string
	describeOut  *acm.DescribeCertificateOutput
	describeErr  error
	deleteArns   []string
	deleteErr    error
}

func (f *fakeACMDeployClient) ImportCertificate(ctx context.Context, params *acm.ImportCertificateInput, optFns ...func(*acm.Options)) (*acm.ImportCertificateOutput, error) {
	f.importCalls = append(f.importCalls, params)
	f.importKeys = append(f.importKeys, append([]byte(nil), params.PrivateKey...))
	if f.importErr != nil {
		return nil, f.importErr
	}
	if f.importOut != nil {
		return f.importOut, nil
	}
	return &acm.ImportCertificateOutput{CertificateArn: aws.String(certDeployTestARN("us-east-1"))}, nil
}

func (f *fakeACMDeployClient) DescribeCertificate(ctx context.Context, params *acm.DescribeCertificateInput, optFns ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error) {
	f.describeArns = append(f.describeArns, aws.ToString(params.CertificateArn))
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if f.describeOut != nil {
		return f.describeOut, nil
	}
	return &acm.DescribeCertificateOutput{Certificate: &acmtypes.CertificateDetail{
		Type: acmtypes.CertificateTypeImported,
	}}, nil
}

func (f *fakeACMDeployClient) DeleteCertificate(ctx context.Context, params *acm.DeleteCertificateInput, optFns ...func(*acm.Options)) (*acm.DeleteCertificateOutput, error) {
	f.deleteArns = append(f.deleteArns, aws.ToString(params.CertificateArn))
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &acm.DeleteCertificateOutput{}, nil
}

// fakeCloudFrontBindClient CloudFront 绑定面 fake：预置分发现配置与 ETag，记录更新请求
type fakeCloudFrontBindClient struct {
	getOut      *cloudfront.GetDistributionOutput
	getErr      error
	updateCalls []*cloudfront.UpdateDistributionInput
	updateErr   error
}

func (f *fakeCloudFrontBindClient) GetDistribution(ctx context.Context, params *cloudfront.GetDistributionInput, optFns ...func(*cloudfront.Options)) (*cloudfront.GetDistributionOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getOut, nil
}

func (f *fakeCloudFrontBindClient) UpdateDistribution(ctx context.Context, params *cloudfront.UpdateDistributionInput, optFns ...func(*cloudfront.Options)) (*cloudfront.UpdateDistributionOutput, error) {
	f.updateCalls = append(f.updateCalls, params)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &cloudfront.UpdateDistributionOutput{}, nil
}

// fakeELBBindClient ELBv2 绑定面 fake：预置监听器证书分页，记录追加请求
type fakeELBBindClient struct {
	certPages    [][]elbv2types.Certificate
	describeErr  error
	describeCall int
	addCalls     []*elbv2.AddListenerCertificatesInput
	addErr       error
}

func (f *fakeELBBindClient) DescribeListenerCertificates(ctx context.Context, params *elbv2.DescribeListenerCertificatesInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeListenerCertificatesOutput, error) {
	f.describeCall++
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	idx := 0
	if params.Marker != nil && *params.Marker != "" {
		idx = 1
	}
	if idx >= len(f.certPages) {
		return &elbv2.DescribeListenerCertificatesOutput{}, nil
	}
	var next *string
	if idx+1 < len(f.certPages) {
		next = aws.String("m1")
	}
	return &elbv2.DescribeListenerCertificatesOutput{
		Certificates: f.certPages[idx],
		NextMarker:   next,
	}, nil
}

func (f *fakeELBBindClient) AddListenerCertificates(ctx context.Context, params *elbv2.AddListenerCertificatesInput, optFns ...func(*elbv2.Options)) (*elbv2.AddListenerCertificatesOutput, error) {
	f.addCalls = append(f.addCalls, params)
	if f.addErr != nil {
		return nil, f.addErr
	}
	return &elbv2.AddListenerCertificatesOutput{}, nil
}

// ==================== UploadCert：上传与 us-east-1 地域硬约束 ====================

// CloudFront 证书上传地域恒 us-east-1（Hard Rule 单测锁定）；证书束拆分：叶证书
// 入 Certificate、其余 CERTIFICATE 块入 CertificateChain；上传名经 Name 标签承载。
func TestCertAdapterUploadCertCloudFrontRegionLocked(t *testing.T) {
	adapter := newTestCertAdapter(t)
	leaf, _ := genTestCertPEM(t)
	intermediate, _ := genTestCertPEM(t)
	fake := &fakeACMDeployClient{}
	var gotRegion string
	adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
		gotRegion = region
		return fake, nil
	}

	arn, err := adapter.UploadCert(context.Background(), certDeployRegionCreds(), CertProductCDN,
		"ecam-ab12cd34-1765432100-0a1b", leaf+intermediate, "-----BEGIN PRIVATE KEY-----\nZmFrZQ==\n-----END PRIVATE KEY-----\n")
	require.NoError(t, err)
	assert.Equal(t, certCloudFrontUploadRegion, gotRegion, "CloudFront 证书上传地域恒 us-east-1（AWS 硬约束，账号主地域不参与路由）")
	assert.Equal(t, certDeployTestARN("us-east-1"), arn)

	require.Len(t, fake.importCalls, 1)
	input := fake.importCalls[0]
	assert.Equal(t, leaf, string(input.Certificate), "首个 CERTIFICATE 块为叶证书")
	assert.Equal(t, intermediate, string(input.CertificateChain), "其余证书块归入证书链")
	require.Len(t, input.Tags, 1)
	assert.Equal(t, "Name", aws.ToString(input.Tags[0].Key))
	assert.Equal(t, "ecam-ab12cd34-1765432100-0a1b", aws.ToString(input.Tags[0].Value), "上传名经 Name 标签承载（ACM 无证书名字段）")
	require.Len(t, fake.importKeys, 1)
	assert.NotEmpty(t, fake.importKeys[0], "私钥明文调用时点经内存透传")

	// 私钥明文用后归零：fake 持有的同一底层切片在调用返回后必须全零（Hard Rule）。
	assert.True(t, allZeroBytes(input.PrivateKey), "ImportCertificate 构参私钥明文用后 Zeroize")
}

// ALB/NLB 上传走账号主地域（其余产品可用账号主地域）；私钥仅内存透传。
func TestCertAdapterUploadCertRegionRoutingOtherProducts(t *testing.T) {
	for _, product := range []string{CertProductALB, CertProductNLB} {
		adapter := newTestCertAdapter(t)
		leaf, _ := genTestCertPEM(t)
		fake := &fakeACMDeployClient{}
		var gotRegion string
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			gotRegion = region
			return fake, nil
		}

		_, err := adapter.UploadCert(context.Background(), certDeployRegionCreds(), product, "ecam-n1", leaf, "fake-key")
		require.NoError(t, err, product)
		assert.Equal(t, "eu-west-1", gotRegion, product+" 上传使用账号主地域")

		require.Len(t, fake.importCalls, 1)
		require.Len(t, fake.importKeys, 1)
		assert.Equal(t, "fake-key", string(fake.importKeys[0]), "私钥明文调用时点仅内存透传")
		assert.True(t, allZeroBytes(fake.importCalls[0].PrivateKey), product+" 私钥明文用后 Zeroize")
		assert.Empty(t, fake.importCalls[0].CertificateChain, "单证书束无证书链")
	}
}

// 上传校验分支：未支持产品/空凭证/空材料不触达云侧；限流映射哨兵；无效证书束显式失败。
func TestCertAdapterUploadCertValidations(t *testing.T) {
	leaf, _ := genTestCertPEM(t)

	t.Run("未支持产品与空材料不构造云客户端", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			t.Fatal("校验失败路径不得构造 ACM 云客户端")
			return nil, nil
		}
		ctx := context.Background()
		creds := certTestCreds()

		for _, product := range []string{"waf", "dcdn", "clb", ""} {
			_, err := adapter.UploadCert(ctx, creds, product, "n", leaf, "k")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrCertProductNotSupported, product)
		}
		_, err := adapter.UploadCert(ctx, nil, CertProductCDN, "n", leaf, "k")
		require.Error(t, err)
		_, err = adapter.UploadCert(ctx, creds, CertProductCDN, "", leaf, "k")
		require.Error(t, err)
		_, err = adapter.UploadCert(ctx, creds, CertProductCDN, "n", leaf, "")
		require.Error(t, err)
		_, err = adapter.UploadCert(ctx, creds, CertProductCDN, "n", "", "k")
		require.Error(t, err)
	})

	t.Run("无效证书束显式失败不猜测", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "n", "not a pem", "k")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CERTIFICATE")
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			return &fakeACMDeployClient{importErr: errCertThrottled}, nil
		}
		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "n", leaf, "k")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
		assert.ErrorIs(t, err, cloudx.ErrCloudRateLimited)
	})

	t.Run("空ARN响应显式失败", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			return &fakeACMDeployClient{importOut: &acm.ImportCertificateOutput{}}, nil
		}
		_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "n", leaf, "k")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty certificate arn")
	})
}

// 私钥明文不出现在错误文案（错误为静态文案+产品上下文，Hard Rule）。
func TestCertAdapterUploadCertKeyNotInError(t *testing.T) {
	adapter := newTestCertAdapter(t)
	leaf, _ := genTestCertPEM(t)
	const keyMaterial = "SUPER-SECRET-KEY-PLAINTEXT"
	adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
		return &fakeACMDeployClient{importErr: &smithy.GenericAPIError{Code: "ValidationException", Message: "invalid parameters"}}, nil
	}
	_, err := adapter.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "n", leaf, keyMaterial)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), keyMaterial, "私钥明文禁入错误文案")
}

// allZeroBytes 判定字节切片是否全零（私钥用后归零断言）
func allZeroBytes(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// ==================== BindResource：CloudFront 分发绑定 ====================

// CloudFront 绑定：读分发配置与 ETag → 仅改 ViewerCertificate（ACM ARN + acm 来源 +
// sni-only + 非默认证书 + 缺省协议版本），Aliases（CNAME 配置面）不改动。
func TestCertAdapterBindCloudFront(t *testing.T) {
	adapter := newTestCertAdapter(t)
	certArn := certDeployTestARN("us-east-1")
	fake := &fakeCloudFrontBindClient{
		getOut: &cloudfront.GetDistributionOutput{
			Distribution: &cftypes.Distribution{DistributionConfig: &cftypes.DistributionConfig{
				Aliases: &cftypes.Aliases{Quantity: aws.Int32(1), Items: []string{"www.example.com"}},
				ViewerCertificate: &cftypes.ViewerCertificate{
					CloudFrontDefaultCertificate: aws.Bool(true),
					MinimumProtocolVersion:       cftypes.MinimumProtocolVersionTLSv1,
				},
			}},
			ETag: aws.String("ETAG1"),
		},
	}
	adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
		return fake, nil
	}

	err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", certArn)
	require.NoError(t, err)

	require.Len(t, fake.updateCalls, 1)
	update := fake.updateCalls[0]
	assert.Equal(t, "E1ABCDEF", aws.ToString(update.Id))
	assert.Equal(t, "ETAG1", aws.ToString(update.IfMatch), "并发控制：IfMatch 取 GetDistribution ETag")
	require.NotNil(t, update.DistributionConfig)
	require.NotNil(t, update.DistributionConfig.ViewerCertificate)
	vc := update.DistributionConfig.ViewerCertificate
	assert.Equal(t, certArn, aws.ToString(vc.ACMCertificateArn))
	assert.Equal(t, cftypes.CertificateSourceAcm, vc.CertificateSource)
	assert.Equal(t, cftypes.SSLSupportMethodSniOnly, vc.SSLSupportMethod)
	assert.False(t, aws.ToBool(vc.CloudFrontDefaultCertificate), "默认证书位必须显式关闭")
	assert.Equal(t, cftypes.MinimumProtocolVersionTLSv122021, vc.MinimumProtocolVersion,
		"默认证书专用协议版本（TLSv1/SSLv3）升到自定义证书兼容缺省值")
	assert.NotNil(t, update.DistributionConfig.Aliases)
	assert.Equal(t, []string{"www.example.com"}, update.DistributionConfig.Aliases.Items,
		"Aliases 属 CNAME 配置面，证书绑定不改动")
}

// 幂等：查看器证书已引用目标 ARN 时不重复提交分发更新。
func TestCertAdapterBindCloudFrontIdempotent(t *testing.T) {
	adapter := newTestCertAdapter(t)
	certArn := certDeployTestARN("us-east-1")
	fake := &fakeCloudFrontBindClient{
		getOut: &cloudfront.GetDistributionOutput{
			Distribution: &cftypes.Distribution{DistributionConfig: &cftypes.DistributionConfig{
				ViewerCertificate: &cftypes.ViewerCertificate{ACMCertificateArn: aws.String(certArn)},
			}},
			ETag: aws.String("ETAG2"),
		},
	}
	adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
		return fake, nil
	}

	err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", certArn)
	require.NoError(t, err)
	assert.Empty(t, fake.updateCalls, "已引用目标证书 → 幂等 no-op（回滚重放/重复绑定安全）")
}

// 已是自定义证书兼容协议（TLSv1_2016+）时保留原最低协议版本。
func TestCertAdapterBindCloudFrontPreservesCustomProtocol(t *testing.T) {
	adapter := newTestCertAdapter(t)
	oldArn := "arn:aws:acm:us-east-1:123456789012:certificate/old"
	newArn := "arn:aws:acm:us-east-1:123456789012:certificate/new"
	fake := &fakeCloudFrontBindClient{
		getOut: &cloudfront.GetDistributionOutput{
			Distribution: &cftypes.Distribution{DistributionConfig: &cftypes.DistributionConfig{
				ViewerCertificate: &cftypes.ViewerCertificate{
					ACMCertificateArn:      aws.String(oldArn),
					MinimumProtocolVersion: cftypes.MinimumProtocolVersionTLSv122019,
				},
			}},
			ETag: aws.String("ETAG3"),
		},
	}
	adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
		return fake, nil
	}

	err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", newArn)
	require.NoError(t, err)
	require.Len(t, fake.updateCalls, 1)
	vc := fake.updateCalls[0].DistributionConfig.ViewerCertificate
	assert.Equal(t, newArn, aws.ToString(vc.ACMCertificateArn), "查看器证书替换为新 ARN")
	assert.Equal(t, cftypes.MinimumProtocolVersionTLSv122019, vc.MinimumProtocolVersion,
		"既有自定义证书兼容协议版本保留")
}

// CloudFront 绑定校验分支：非 ARN 形态 / 跨地域证书 / 空 ID / 分发缺失 / 限流。
func TestCertAdapterBindCloudFrontValidation(t *testing.T) {
	t.Run("非ACM ARN证书ID显式拒绝", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
			t.Fatal("IAM 托管形态不得触达云客户端")
			return nil, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", "AS1IAMID")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not an acm arn")
	})

	t.Run("跨地域ACM证书显式拒绝", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
			t.Fatal("地域校验先于云客户端构造")
			return nil, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", certDeployTestARN("us-west-2"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "us-east-1")
	})

	t.Run("空凭证与空ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		ctx := context.Background()
		require.Error(t, adapter.BindResource(ctx, nil, CertProductCDN, "E1ABCDEF", certDeployTestARN("us-east-1")))
		require.Error(t, adapter.BindResource(ctx, certTestCreds(), CertProductCDN, "", certDeployTestARN("us-east-1")))
		require.Error(t, adapter.BindResource(ctx, certTestCreds(), CertProductCDN, "E1ABCDEF", " "))
	})

	t.Run("分发不存在错误透传", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
			return &fakeCloudFrontBindClient{getErr: errCertNotFound}, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", certDeployTestARN("us-east-1"))
		require.Error(t, err)
	})

	t.Run("更新限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
			return &fakeCloudFrontBindClient{
				getOut: &cloudfront.GetDistributionOutput{
					Distribution: &cftypes.Distribution{DistributionConfig: &cftypes.DistributionConfig{}},
					ETag:         aws.String("ETAG4"),
				},
				updateErr: errCertThrottled,
			}, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", certDeployTestARN("us-east-1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空ETag显式失败", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCloudFrontBindClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontBindCertAPI, error) {
			return &fakeCloudFrontBindClient{
				getOut: &cloudfront.GetDistributionOutput{
					Distribution: &cftypes.Distribution{DistributionConfig: &cftypes.DistributionConfig{}},
				},
			}, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductCDN, "E1ABCDEF", certDeployTestARN("us-east-1"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty etag")
	})
}

// ==================== BindResource：ALB/NLB 监听器证书绑定 ====================

// ALB/NLB 绑定：AddListenerCertificates 形态（ALB HTTPS 与 NLB TLS 监听器同 API），
// 按监听器 ARN 地域构建客户端；追加时不设 IsDefault（默认位不改动）。
func TestCertAdapterBindELBListener(t *testing.T) {
	for _, product := range []string{CertProductALB, CertProductNLB} {
		adapter := newTestCertAdapter(t)
		listenerArn := "arn:aws:elasticloadbalancing:eu-west-1:123456789012:listener/app/my-alb/abc/l1"
		certArn := certDeployTestARN("eu-west-1")
		fake := &fakeELBBindClient{}
		var gotRegion string
		adapter.newElbBindClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
			gotRegion = region
			return fake, nil
		}

		err := adapter.BindResource(context.Background(), certTestCreds(), product, listenerArn, certArn)
		require.NoError(t, err, product)
		assert.Equal(t, "eu-west-1", gotRegion, product+" 按监听器 ARN 地域构建客户端")

		require.Len(t, fake.addCalls, 1, product)
		assert.Equal(t, listenerArn, aws.ToString(fake.addCalls[0].ListenerArn))
		require.Len(t, fake.addCalls[0].Certificates, 1)
		assert.Equal(t, certArn, aws.ToString(fake.addCalls[0].Certificates[0].CertificateArn))
		assert.Nil(t, fake.addCalls[0].Certificates[0].IsDefault, "追加证书不设默认位（AddListenerCertificates 契约）")
	}
}

// 幂等：证书已在监听器证书集（含分页遍历命中）→ 不重复追加。
func TestCertAdapterBindELBListenerIdempotent(t *testing.T) {
	adapter := newTestCertAdapter(t)
	listenerArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/net/my-nlb/xyz/l9"
	certArn := certDeployTestARN("us-east-1")
	fake := &fakeELBBindClient{
		certPages: [][]elbv2types.Certificate{
			{{CertificateArn: aws.String("arn:aws:acm:us-east-1:123456789012:certificate/other")}},
			{{CertificateArn: aws.String(certArn)}}, // 第二页命中（默认证书+扩展证书分页形态）
		},
	}
	adapter.newElbBindClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
		return fake, nil
	}

	err := adapter.BindResource(context.Background(), certTestCreds(), CertProductNLB, listenerArn, certArn)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.describeCall, "分页遍历至命中页")
	assert.Empty(t, fake.addCalls, "已挂载 → 幂等 no-op（回滚重放安全）")
}

// 监听器绑定校验分支：地域不一致 / 非 ARN 形态 / 空 ID / 限流。
func TestCertAdapterBindELBListenerValidation(t *testing.T) {
	listenerArn := "arn:aws:elasticloadbalancing:eu-west-1:123456789012:listener/app/my-alb/abc/l1"

	t.Run("证书与监听器跨地域显式失败不猜测", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newElbBindClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
			t.Fatal("地域校验先于云客户端构造")
			return nil, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, listenerArn, certDeployTestARN("us-east-1"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "differs from")
	})

	t.Run("非ACM ARN证书ID显式拒绝", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, listenerArn, "AS1IAMID")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not an acm arn")
	})

	t.Run("空凭证与空ID不触达云侧", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newElbBindClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
			t.Fatal("校验失败路径不得构造 ELB 云客户端")
			return nil, nil
		}
		ctx := context.Background()
		require.Error(t, adapter.BindResource(ctx, nil, CertProductALB, listenerArn, certDeployTestARN("eu-west-1")))
		require.Error(t, adapter.BindResource(ctx, certTestCreds(), CertProductALB, " ", certDeployTestARN("eu-west-1")))
		require.Error(t, adapter.BindResource(ctx, certTestCreds(), CertProductALB, listenerArn, ""))
	})

	t.Run("追加限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newElbBindClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbBindCertAPI, error) {
			return &fakeELBBindClient{addErr: errCertThrottled}, nil
		}
		err := adapter.BindResource(context.Background(), certTestCreds(), CertProductALB, listenerArn, certDeployTestARN("eu-west-1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("未支持产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		err := adapter.BindResource(context.Background(), certTestCreds(), "waf", listenerArn, certDeployTestARN("eu-west-1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})
}

// ==================== CleanupOrphan：导入证书删除（托管证书显式识别拒绝） ====================

func TestCertAdapterCleanupOrphan(t *testing.T) {
	certArn := certDeployTestARN("us-east-1")

	t.Run("导入证书删除成功且按ARN地域构建客户端", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeACMDeployClient{}
		var gotRegion string
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			gotRegion = region
			return fake, nil
		}
		err := adapter.CleanupOrphan(context.Background(), certTestCreds(), certDeployTestARN("eu-central-1"))
		require.NoError(t, err)
		assert.Equal(t, "eu-central-1", gotRegion, "ACM 区域资源：按证书 ARN 地域构建客户端")
		assert.Equal(t, []string{certDeployTestARN("eu-central-1")}, fake.describeArns)
		assert.Equal(t, []string{certDeployTestARN("eu-central-1")}, fake.deleteArns, "IMPORTED 类型执行删除")
	})

	t.Run("已删除幂等成功不触发删除", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeACMDeployClient{describeErr: errCertNotFound}
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			return fake, nil
		}
		err := adapter.CleanupOrphan(context.Background(), certTestCreds(), certArn)
		require.NoError(t, err, "已不存在 → 幂等成功（清理队列重放安全）")
		assert.Empty(t, fake.deleteArns)
	})

	t.Run("AWS托管证书显式识别拒绝删除", func(t *testing.T) {
		for _, managed := range []acmtypes.CertificateType{acmtypes.CertificateTypeAmazonIssued, acmtypes.CertificateTypePrivate} {
			adapter := newTestCertAdapter(t)
			fake := &fakeACMDeployClient{describeOut: &acm.DescribeCertificateOutput{
				Certificate: &acmtypes.CertificateDetail{Type: managed},
			}}
			adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
				return fake, nil
			}
			err := adapter.CleanupOrphan(context.Background(), certTestCreds(), certArn)
			require.Error(t, err, string(managed))
			assert.Contains(t, err.Error(), "not orphan cleanup targets", string(managed))
			assert.Empty(t, fake.deleteArns, "托管证书不得删除")
		}
	})

	t.Run("删除间隙不存在幂等成功", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeACMDeployClient{deleteErr: errCertNotFound}
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			return fake, nil
		}
		err := adapter.CleanupOrphan(context.Background(), certTestCreds(), certArn)
		require.NoError(t, err, "描述与删除间隙被并发删除 → 幂等成功")
	})

	t.Run("非ARN形态与空凭证不触达云侧", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			t.Fatal("非 ARN 形态不得构造 ACM 云客户端")
			return nil, nil
		}
		ctx := context.Background()
		require.Error(t, adapter.CleanupOrphan(ctx, certTestCreds(), "AS1IAMID"), "IAM 托管证书 ID 非 ACM 库资源")
		require.Error(t, adapter.CleanupOrphan(ctx, certTestCreds(), " "))
		require.Error(t, adapter.CleanupOrphan(ctx, nil, certArn))
	})

	t.Run("限流映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newAcmDeployClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmDeployCertAPI, error) {
			return &fakeACMDeployClient{describeErr: errCertThrottled}, nil
		}
		err := adapter.CleanupOrphan(context.Background(), certTestCreds(), certArn)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})
}

// ==================== 五方法面完整性（复用发现适配只读实现） ====================

// 完整适配器在单实例上具备五方法：ListReferences/GetCert 继承发现适配真实实现
// （非哨兵），写方法为部署面实现。
func TestCertAdapterFiveMethodSurface(t *testing.T) {
	adapter := newTestCertAdapter(t)
	ctx := context.Background()

	// ListReferences 继承：fake CloudFront 列举 → 引用产出（非 ErrDiscoveryOnly）
	fakeCF := &fakeCloudFrontClient{pages: [][]cftypes.DistributionSummary{{
		{Id: aws.String("E1A"), ViewerCertificate: &cftypes.ViewerCertificate{ACMCertificateArn: aws.String(certDeployTestARN("us-east-1"))}},
	}}}
	adapter.newCloudFrontClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontCertAPI, error) {
		return fakeCF, nil
	}
	refs, err := adapter.ListReferences(ctx, certTestCreds(), CertProductCDN)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "E1A", refs[0].ResourceID)

	// GetCert 继承：ACM GetCertificate → SHA256 指纹对齐口径（回滚目标校验依据）
	leaf, fingerprint := genTestCertPEM(t)
	fakeACM := &fakeACMClient{cert: aws.String(leaf)}
	adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
		return fakeACM, nil
	}
	info, err := adapter.GetCert(ctx, certTestCreds(), certDeployTestARN("us-east-1"))
	require.NoError(t, err)
	assert.True(t, info.Exists)
	assert.Equal(t, fingerprint, info.Fingerprint, "SHA256 指纹对齐台账口径")

	// 与发现适配同源限流器（共享 20 QPS 口径，测试放宽后可见）
	assert.NotNil(t, adapter.rateLimiter)
}

// 部署面真实客户端工厂离线构建（静态凭证配置加载不发起网络请求，与发现适配
// 工厂同口径验证）；CloudFront 固定 us-east-1 接入、ACM/ELB 空地域回退缺省值。
func TestCertAdapterRealClientFactories(t *testing.T) {
	creds := certTestCreds()
	ctx := context.Background()

	acmClient, err := newCertDeployACMClient(ctx, creds, "us-east-1")
	require.NoError(t, err)
	assert.NotNil(t, acmClient)
	acmClientDefault, err := newCertDeployACMClient(ctx, creds, "")
	require.NoError(t, err)
	assert.NotNil(t, acmClientDefault)

	cfClient, err := newCertDeployCloudFrontClient(ctx, creds)
	require.NoError(t, err)
	assert.NotNil(t, cfClient)

	elbClient, err := newCertDeployELBClient(ctx, creds, "eu-west-1")
	require.NoError(t, err)
	assert.NotNil(t, elbClient)
	elbClientDefault, err := newCertDeployELBClient(ctx, creds, "")
	require.NoError(t, err)
	assert.NotNil(t, elbClientDefault)

	// 边界：nil logger 回退默认日志组件
	assert.NotNil(t, NewCertAdapter(nil))
}
