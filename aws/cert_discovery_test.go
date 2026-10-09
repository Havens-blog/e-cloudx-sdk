package aws

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk"
	"github.com/Havens-blog/e-cloudx-sdk/common/ratelimit"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
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

// newTestCertDiscoveryAdapter 构造可注入 fake SDK 客户端的发现适配器（限流器放宽避免测试排队）
func newTestCertDiscoveryAdapter(t *testing.T) *CertDiscoveryAdapter {
	t.Helper()
	adapter := NewCertDiscoveryAdapter(elog.DefaultLogger)
	adapter.rateLimiter = ratelimit.NewRateLimiter(5000)
	return adapter
}

func certTestCreds() *domain.CloudAccount {
	return &domain.CloudAccount{
		Name:            "aws-main",
		Provider:        domain.CloudProviderAWS,
		AccessKeyID:     "test-ak",
		AccessKeySecret: "test-sk",
		Regions:         []string{"us-east-1", "us-west-2"},
	}
}

var (
	errCertThrottled = &smithy.GenericAPIError{Code: "TooManyRequestsException", Message: "Rate exceeded"}
	errCertNotFound  = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "Certificate not found"}
)

// certTestBool bool 指针构造（避免与既有测试辅助重名）
func certTestBool(v bool) *bool { return &v }

// fakeCloudFrontClient CloudFront fake（按 Marker 链返回预置页）
type fakeCloudFrontClient struct {
	pages [][]cftypes.DistributionSummary
	calls int
	err   error
}

func (f *fakeCloudFrontClient) ListDistributions(ctx context.Context, input *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	idx := 0
	if input.Marker != nil && *input.Marker != "" {
		if _, err := parseMarkerIndex(*input.Marker); err != nil {
			return nil, err
		}
		idx = 1
	}
	if idx >= len(f.pages) {
		return &cloudfront.ListDistributionsOutput{DistributionList: &cftypes.DistributionList{}}, nil
	}
	items := f.pages[idx]
	var next *string
	if idx+1 < len(f.pages) {
		next = aws.String("m1")
	}
	return &cloudfront.ListDistributionsOutput{
		DistributionList: &cftypes.DistributionList{
			Items:       items,
			IsTruncated: certTestBool(idx+1 < len(f.pages)),
			NextMarker:  next,
		},
	}, nil
}

func parseMarkerIndex(marker string) (int, error) {
	if marker != "m1" {
		return 0, &smithy.GenericAPIError{Code: "InvalidArgument", Message: "bad marker " + marker}
	}
	return 1, nil
}

// fakeELBClient ELBv2 fake（LB 列表 + 监听器按 LB ARN 分发）
type fakeELBClient struct {
	lbs             []elbv2types.LoadBalancer
	listeners       map[string][]elbv2types.Listener
	listenerErrs    map[string]error
	lbErr           error
	describeLBCalls int
	listenerCalls   int
}

func (f *fakeELBClient) DescribeLoadBalancers(ctx context.Context, input *elbv2.DescribeLoadBalancersInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error) {
	f.describeLBCalls++
	if f.lbErr != nil {
		return nil, f.lbErr
	}
	return &elbv2.DescribeLoadBalancersOutput{LoadBalancers: f.lbs}, nil
}

func (f *fakeELBClient) DescribeListeners(ctx context.Context, input *elbv2.DescribeListenersInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeListenersOutput, error) {
	f.listenerCalls++
	if input.LoadBalancerArn == nil {
		return nil, &smithy.GenericAPIError{Code: "ValidationError", Message: "missing arn"}
	}
	if err, ok := f.listenerErrs[*input.LoadBalancerArn]; ok {
		return nil, err
	}
	return &elbv2.DescribeListenersOutput{Listeners: f.listeners[*input.LoadBalancerArn]}, nil
}

// fakeACMClient ACM fake（返回预置 PEM 或错误；cert=叶证书，chain=证书链）
type fakeACMClient struct {
	cert    *string
	chain   *string
	err     error
	calls   int
	lastArn string
}

func (f *fakeACMClient) GetCertificate(ctx context.Context, input *acm.GetCertificateInput, optFns ...func(*acm.Options)) (*acm.GetCertificateOutput, error) {
	f.calls++
	f.lastArn = aws.ToString(input.CertificateArn)
	if f.err != nil {
		return nil, f.err
	}
	return &acm.GetCertificateOutput{Certificate: f.cert, CertificateChain: f.chain}, nil
}

// genTestCertPEM 生成自签测试证书，返回 PEM 与 SHA256 指纹（hex 小写）
func genTestCertPEM(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cert-test.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{"cert-test.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(sum[:])
}

// ==================== 只读硬约束：三写方法返回哨兵且不触达云侧 ====================

func TestCertDiscoveryWritesReturnSentinel(t *testing.T) {
	t.Run("三写方法一律返回ErrDiscoveryOnly且不构造云客户端", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		failFactory := func(name string) {
			t.Helper()
			t.Fatalf("discovery-only 写方法不得构造 %s 云客户端", name)
		}
		adapter.newCloudFrontClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontCertAPI, error) {
			failFactory("CloudFront")
			return nil, nil
		}
		adapter.newElbClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			failFactory("ELBv2")
			return nil, nil
		}
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			failFactory("ACM")
			return nil, nil
		}

		ctx := context.Background()
		creds := certTestCreds()
		for _, product := range []string{CertProductCDN, CertProductALB, CertProductNLB} {
			_, err := adapter.UploadCert(ctx, creds, product, "n", "pem", "key")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)
			assert.ErrorIs(t, err, cloudx.ErrDiscoveryOnly, product)
			assert.NotErrorIs(t, err, cloudx.ErrCloudRateLimited, product)

			err = adapter.BindResource(ctx, creds, product, "res-1", "arn-1")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)

			err = adapter.CleanupOrphan(ctx, creds, "arn-1")
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrDiscoveryOnly, product)
		}
	})
}

// ==================== ListReferences: CDN (CloudFront) ====================

func TestCertDiscoveryListCDNReferences(t *testing.T) {
	t.Run("分页遍历并产出ACM与IAM形态引用", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeCloudFrontClient{pages: [][]cftypes.DistributionSummary{
			{
				{Id: aws.String("E1A"), ViewerCertificate: &cftypes.ViewerCertificate{ACMCertificateArn: aws.String("arn:aws:acm:us-east-1:1:certificate/c1")}},
				{Id: aws.String("E1B"), ViewerCertificate: &cftypes.ViewerCertificate{IAMCertificateId: aws.String("AS1ABC")}},
			},
			{
				{Id: aws.String("E2A"), ViewerCertificate: &cftypes.ViewerCertificate{CloudFrontDefaultCertificate: certTestBool(true)}},
			},
		}}
		adapter.newCloudFrontClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontCertAPI, error) { return fake, nil }

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.NoError(t, err)
		require.Len(t, refs, 2)        // 默认证书不构成自持引用
		assert.Equal(t, 2, fake.calls) // 翻页两次
		assert.Equal(t, "aws", refs[0].Cloud)
		assert.Equal(t, CertProductCDN, refs[0].Product)
		assert.Equal(t, "E1A", refs[0].ResourceID)
		assert.Equal(t, "arn:aws:acm:us-east-1:1:certificate/c1", refs[0].ReferencedCloudCertID)
		assert.Equal(t, "aws-main", refs[0].AccountKey)
		assert.Equal(t, "AS1ABC", refs[1].ReferencedCloudCertID)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newCloudFrontClient = func(ctx context.Context, creds *domain.CloudAccount) (cloudFrontCertAPI, error) {
			return &fakeCloudFrontClient{err: errCertThrottled}, nil
		}
		_, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
		assert.ErrorIs(t, err, cloudx.ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductCDN)
		require.Error(t, err)
	})
}

// ==================== ListReferences: ALB / NLB (ELBv2) ====================

func TestCertDiscoveryListELBReferences(t *testing.T) {
	albArn := "arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/my-alb/abc"
	nlbArn := "arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/net/my-nlb/xyz"

	t.Run("按负载均衡类型过滤并展开监听器证书", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeELBClient{
			lbs: []elbv2types.LoadBalancer{
				{LoadBalancerArn: aws.String(albArn), Type: elbv2types.LoadBalancerTypeEnumApplication},
				{LoadBalancerArn: aws.String(nlbArn), Type: elbv2types.LoadBalancerTypeEnumNetwork},
				{LoadBalancerArn: aws.String("arn:gateway"), Type: elbv2types.LoadBalancerTypeEnumGateway},
			},
			listeners: map[string][]elbv2types.Listener{
				albArn: {
					{ListenerArn: aws.String(albArn + "/listener/l1"), Certificates: []elbv2types.Certificate{
						{CertificateArn: aws.String("arn:aws:acm:us-east-1:1:certificate/a1")},
						{CertificateArn: aws.String("arn:aws:acm:us-east-1:1:certificate/a2")}, // SNI 扩展证书
					}},
				},
				nlbArn: {
					{ListenerArn: aws.String(nlbArn + "/listener/l9"), Certificates: []elbv2types.Certificate{
						{CertificateArn: aws.String("arn:aws:acm:us-east-1:1:certificate/n1")},
					}},
				},
			},
		}
		adapter.newElbClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			return fake, nil
		}

		// alb：两地域 × 1 监听器 × 2 证书 = 4 条（gateway 类型被过滤）
		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.NoError(t, err)
		require.Len(t, refs, 4)
		first := refs[0]
		assert.Equal(t, "aws", first.Cloud)
		assert.Equal(t, CertProductALB, first.Product)
		assert.Equal(t, albArn+"/listener/l1", first.ResourceID)
		assert.Equal(t, "arn:aws:acm:us-east-1:1:certificate/a1", first.ReferencedCloudCertID)
		assert.Equal(t, "aws-main", first.AccountKey)

		// nlb：两地域 × 1 监听器 × 1 证书 = 2 条
		refs, err = adapter.ListReferences(context.Background(), certTestCreds(), CertProductNLB)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		assert.Equal(t, CertProductNLB, refs[0].Product)
		assert.Equal(t, "arn:aws:acm:us-east-1:1:certificate/n1", refs[0].ReferencedCloudCertID)
	})

	t.Run("单实例监听器失败跳过不中断整体扫描", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		fake := &fakeELBClient{
			lbs: []elbv2types.LoadBalancer{
				{LoadBalancerArn: aws.String(albArn), Type: elbv2types.LoadBalancerTypeEnumApplication},
			},
			listeners:    map[string][]elbv2types.Listener{},
			listenerErrs: map[string]error{albArn: errCertThrottled},
		}
		adapter.newElbClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			return fake, nil
		}

		refs, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.NoError(t, err) // 单实例失败不构成整体错误
		assert.Empty(t, refs)
	})

	t.Run("LB列举限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newElbClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (elbCertAPI, error) {
			return &fakeELBClient{lbErr: errCertThrottled}, nil
		}
		_, err := adapter.ListReferences(context.Background(), certTestCreds(), CertProductALB)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.ListReferences(context.Background(), nil, CertProductALB)
		require.Error(t, err)
	})
}

// ==================== GetCert (ACM) ====================

func TestCertDiscoveryGetCert(t *testing.T) {
	arn := "arn:aws:acm:us-east-1:1:certificate/c1"

	t.Run("返回PEM解析的SHA256指纹与有效期", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		pemStr, fingerprint := genTestCertPEM(t)
		fake := &fakeACMClient{cert: aws.String(pemStr)}
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return fake, nil
		}

		info, err := adapter.GetCert(context.Background(), certTestCreds(), arn)
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, fingerprint, info.Fingerprint)
		assert.True(t, info.NotAfter.After(time.Now().Add(364*24*time.Hour)))
		assert.Equal(t, 1, fake.calls)
		assert.Equal(t, arn, fake.lastArn)
	})

	t.Run("证书不存在返回Exists=false", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return &fakeACMClient{err: errCertNotFound}, nil
		}
		info, err := adapter.GetCert(context.Background(), certTestCreds(), arn)
		require.NoError(t, err)
		assert.False(t, info.Exists)
		assert.Empty(t, info.Fingerprint)
		assert.True(t, info.NotAfter.IsZero())
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return &fakeACMClient{err: errCertThrottled}, nil
		}
		_, err := adapter.GetCert(context.Background(), certTestCreds(), arn)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("非ARN形态证书ID显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.GetCert(context.Background(), certTestCreds(), "AS1ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "IAM-hosted")
	})

	t.Run("IAM-hosted非ARN返回结构化降级标记哨兵", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		// IAM-hosted 判定先于客户端构造：注入失败工厂证明不触达云侧
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			t.Fatalf("IAM-hosted 分支不得构造 ACM 云客户端")
			return nil, nil
		}
		_, err := adapter.GetCert(context.Background(), certTestCreds(), "AS1ABC")
		require.Error(t, err)
		// 可被上层识别为降级标记（非通用失败）
		assert.ErrorIs(t, err, ErrCertPEMUnsupported)
		assert.ErrorIs(t, err, cloudx.ErrCertPEMUnsupported)
		assert.NotErrorIs(t, err, ErrCloudRateLimited)
		// 错误文案为静态文案，不携带云响应片段
		assert.Contains(t, err.Error(), "IAM-hosted")
	})

	t.Run("CertificateChain叶在前拼接为净化fullchain", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		leaf, leafFingerprint := genTestCertPEM(t)
		intermediate, _ := genTestCertPEM(t)
		root, _ := genTestCertPEM(t)
		// 证书链混入私钥块与噪声：拼接净化后必须仅余 CERTIFICATE 块
		keyBlock := "-----BEGIN EC PRIVATE KEY-----\nZmFrZS1rZXk=\n-----END EC PRIVATE KEY-----\n"
		chain := keyBlock + intermediate + root
		fake := &fakeACMClient{cert: aws.String(leaf), chain: aws.String(chain)}
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return fake, nil
		}

		info, err := adapter.GetCert(context.Background(), certTestCreds(), arn)
		require.NoError(t, err)
		assert.True(t, info.Exists)
		// 内容级断言：叶在前拼接、链中私钥被丢弃、不含 PRIVATE KEY
		assert.Equal(t, leaf+intermediate+root, info.CertChainPEM)
		assert.True(t, strings.HasPrefix(info.CertChainPEM, "-----BEGIN CERTIFICATE-----"))
		assert.NotContains(t, info.CertChainPEM, "PRIVATE KEY")
		assert.Equal(t, leafFingerprint, info.Fingerprint)
	})

	t.Run("无CertificateChain时CertChainPEM为净化后的叶证书", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		leaf, _ := genTestCertPEM(t)
		fake := &fakeACMClient{cert: aws.String(leaf)}
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return fake, nil
		}
		info, err := adapter.GetCert(context.Background(), certTestCreds(), arn)
		require.NoError(t, err)
		assert.Equal(t, leaf, info.CertChainPEM)
	})

	t.Run("块尾与下一块头无换行粘连时仍逐块净化", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		leaf, _ := genTestCertPEM(t)
		intermediate, _ := genTestCertPEM(t)
		// ACM 响应缺失块尾换行的防御形态：TrimSpace+补换行保证逐块解析
		gluedLeaf := strings.TrimRight(leaf, "\n")
		fake := &fakeACMClient{cert: aws.String(gluedLeaf), chain: aws.String(intermediate)}
		adapter.newAcmClient = func(ctx context.Context, creds *domain.CloudAccount, region string) (acmCertAPI, error) {
			return fake, nil
		}
		info, err := adapter.GetCert(context.Background(), certTestCreds(), arn)
		require.NoError(t, err)
		assert.Equal(t, leaf+intermediate, info.CertChainPEM)
	})

	t.Run("空证书ID与空凭证显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		_, err := adapter.GetCert(context.Background(), certTestCreds(), " ")
		require.Error(t, err)
		_, err = adapter.GetCert(context.Background(), nil, arn)
		require.Error(t, err)
	})
}

// ==================== 分发、辅助与工厂 ====================

func TestCertDiscoveryDispatch(t *testing.T) {
	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertDiscoveryAdapter(t)
		for _, product := range []string{"waf", "dcdn", "clb", "crd", ""} {
			_, err := adapter.ListReferences(context.Background(), certTestCreds(), product)
			require.Error(t, err, product)
			assert.ErrorIs(t, err, ErrCertProductNotSupported, product)
		}
	})

	t.Run("产品集合覆盖AWS三产品且枚举对齐schema", func(t *testing.T) {
		for _, product := range []string{CertProductCDN, CertProductALB, CertProductNLB} {
			assert.True(t, certSupportedProducts[product], product)
		}
		// schema.sql cert_references.product 枚举约束
		for product := range certSupportedProducts {
			assert.Contains(t, []string{"cdn", "dcdn", "waf", "alb", "clb", "nlb", "crd"}, product)
		}
		// AWS WAF 非 TLS 终结点，明确不入支持集
		assert.False(t, certSupportedProducts["waf"])
	})
}

func TestCertDiscoveryHelpers(t *testing.T) {
	// ARN 地域解析
	assert.Equal(t, "us-east-1", arnRegion("arn:aws:acm:us-east-1:1:certificate/c1"))
	assert.Equal(t, "cn-north-1", arnRegion("arn:aws-cn:acm:cn-north-1:1:certificate/c1"))
	assert.Equal(t, certDefaultRegion, arnRegion("arn:aws:acm::1:certificate/c1"))
	assert.Equal(t, certDefaultRegion, arnRegion("bad-arn"))

	// 查看器证书提取
	assert.Equal(t, "arn", cloudFrontViewerCertID(&cftypes.ViewerCertificate{ACMCertificateArn: aws.String("arn")}))
	assert.Equal(t, "iam", cloudFrontViewerCertID(&cftypes.ViewerCertificate{IAMCertificateId: aws.String("iam")}))
	assert.Empty(t, cloudFrontViewerCertID(&cftypes.ViewerCertificate{CloudFrontDefaultCertificate: certTestBool(true)}))
	assert.Empty(t, cloudFrontViewerCertID(nil))

	// 地域回退
	creds := certTestCreds()
	assert.Equal(t, []string{"us-east-1", "us-west-2"}, certCredsRegions(creds))
	creds.Regions = nil
	assert.Equal(t, []string{certDefaultRegion}, certCredsRegions(creds))
	assert.Equal(t, certDefaultRegion, certCredsRegion(nil))
}

func TestCertDiscoveryRealClientFactories(t *testing.T) {
	// AWS SDK 静态凭证客户端构建不发起网络请求，可离线验证工厂与真实客户端满足接口签名
	creds := certTestCreds()
	adapter := NewCertDiscoveryAdapter(elog.DefaultLogger)
	ctx := context.Background()

	cfClient, err := adapter.newCloudFrontClient(ctx, creds)
	require.NoError(t, err)
	assert.NotNil(t, cfClient)

	elbClient, err := adapter.newElbClient(ctx, creds, "us-east-1")
	require.NoError(t, err)
	assert.NotNil(t, elbClient)

	elbClientDefault, err := adapter.newElbClient(ctx, creds, "")
	require.NoError(t, err)
	assert.NotNil(t, elbClientDefault)

	acmClient, err := adapter.newAcmClient(ctx, creds, "us-east-1")
	require.NoError(t, err)
	assert.NotNil(t, acmClient)

	// 边界：空限流器/零分页回退
	adapter.rateLimiter = nil
	assert.NoError(t, adapter.waitRateLimit(context.Background()))
	adapter.listPageSize = 0
	assert.Equal(t, certDiscoveryPageSize, adapter.certPageSize())
	assert.NotNil(t, NewCertDiscoveryAdapter(nil))
}
