package aliyun

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
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cas"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 测试公共设施 ====================

// newTestCertAdapter 构造可注入 fake SDK 客户端的证书适配器（限流器放宽避免测试排队）
func newTestCertAdapter(t *testing.T) *CertAdapter {
	t.Helper()
	adapter := NewCertAdapter(elog.DefaultLogger)
	limiter := NewRateLimiter(5000)
	adapter.rateLimiter = limiter
	t.Cleanup(limiter.Stop)
	return adapter
}

func testCertCreds() *domain.CloudAccount {
	return &domain.CloudAccount{
		Name:            "aliyun-main",
		Provider:        domain.CloudProviderAliyun,
		AccessKeyID:     "test-ak",
		AccessKeySecret: "test-sk",
		Regions:         []string{"cn-hangzhou", "cn-shanghai"},
	}
}

var errFakeThrottling = errors.New("SDK.ServerError Message: RequestLimitExceeded, request throttling. ErrorCode: Throttling")

// fakeCasClient CAS 证书库 SDK fake
type fakeCasClient struct {
	uploadErr  error
	detailErr  error
	deleteErr  error
	uploadResp *cas.UploadUserCertificateResponse
	detailResp *cas.GetUserCertificateDetailResponse

	uploadReq  *cas.UploadUserCertificateRequest
	detailReq  *cas.GetUserCertificateDetailRequest
	deleteReqs []*cas.DeleteUserCertificateRequest
}

func (f *fakeCasClient) UploadUserCertificate(request *cas.UploadUserCertificateRequest) (*cas.UploadUserCertificateResponse, error) {
	f.uploadReq = request
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	resp := f.uploadResp
	if resp == nil {
		resp = cas.CreateUploadUserCertificateResponse()
		resp.CertId = 8089870
	}
	return resp, nil
}

func (f *fakeCasClient) GetUserCertificateDetail(request *cas.GetUserCertificateDetailRequest) (*cas.GetUserCertificateDetailResponse, error) {
	f.detailReq = request
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	resp := f.detailResp
	if resp == nil {
		resp = cas.CreateGetUserCertificateDetailResponse()
		resp.Fingerprint = "AB:CD:EF:01"
		resp.EndDate = "2027-01-02"
	}
	return resp, nil
}

func (f *fakeCasClient) DeleteUserCertificate(request *cas.DeleteUserCertificateRequest) (*cas.DeleteUserCertificateResponse, error) {
	f.deleteReqs = append(f.deleteReqs, request)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return cas.CreateDeleteUserCertificateResponse(), nil
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

// certChainTestKeyPEM 手写私钥 PEM 块（净化内容级断言用，base64 内容不影响块类型过滤）
const certChainTestKeyPEM = "-----BEGIN EC PRIVATE KEY-----\nZmFrZS1rZXk=\n-----END EC PRIVATE KEY-----\n"

// genTestCertChainPEM 生成叶/中间 CA/自签根三张独立测试证书的 fullchain 材料：
// 返回（含私钥块前缀的原始 bundle，叶在前净化期望序列，叶证书指纹）。
func genTestCertChainPEM(t *testing.T) (rawBundle, wantChain, leafFingerprint string) {
	t.Helper()
	leaf, leafFingerprint := genTestCertPEM(t)
	intermediate, _ := genTestCertPEM(t)
	root, _ := genTestCertPEM(t)
	return certChainTestKeyPEM + leaf + intermediate + root, leaf + intermediate + root, leafFingerprint
}

// ==================== UploadCert ====================

func TestCertAdapterUploadCert(t *testing.T) {
	pemStr := "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"

	t.Run("cdn产品返回CAS数字ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCasClient{}
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) { return fake, nil }

		id, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "cert-2026", pemStr, "key")
		require.NoError(t, err)
		assert.Equal(t, "8089870", id)

		require.NotNil(t, fake.uploadReq)
		assert.Equal(t, "cert-2026", fake.uploadReq.Name)
		assert.Equal(t, pemStr, fake.uploadReq.Cert)
		assert.Equal(t, "key", fake.uploadReq.Key)
	})

	t.Run("alb产品返回带地域后缀ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCasClient{}
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) { return fake, nil }

		id, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductALB, "cert-2026", pemStr, "key")
		require.NoError(t, err)
		assert.Equal(t, "8089870-cn-hangzhou", id)
	})

	t.Run("nlb产品返回带地域后缀ID", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{}, nil
		}
		id, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductNLB, "cert-2026", pemStr, "key")
		require.NoError(t, err)
		assert.Equal(t, "8089870-cn-hangzhou", id)
	})

	t.Run("国际站地域使用ap-southeast-1", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{}, nil
		}
		creds := testCertCreds()
		creds.Regions = []string{"ap-southeast-1"}
		id, err := adapter.UploadCert(context.Background(), creds, CertProductALB, "cert-2026", pemStr, "key")
		require.NoError(t, err)
		assert.Equal(t, "8089870-ap-southeast-1", id)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{uploadErr: errFakeThrottling}, nil
		}
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "cert-2026", pemStr, "key")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), "clb", "n", pemStr, "key")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})

	t.Run("参数缺失显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "", pemStr, "key")
		require.Error(t, err)
		_, err = adapter.UploadCert(context.Background(), testCertCreds(), CertProductCDN, "n", "", "key")
		require.Error(t, err)
		_, err = adapter.UploadCert(context.Background(), nil, CertProductCDN, "n", pemStr, "key")
		require.Error(t, err)
	})
}

// ==================== GetCert ====================

func TestCertAdapterGetCert(t *testing.T) {
	t.Run("返回PEM解析的SHA256指纹与有效期", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		pemStr, fingerprint := genTestCertPEM(t)
		detail := cas.CreateGetUserCertificateDetailResponse()
		detail.Cert = pemStr
		fake := &fakeCasClient{detailResp: detail}
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) { return fake, nil }

		info, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, fingerprint, info.Fingerprint)
		assert.True(t, info.NotAfter.After(time.Now().Add(364*24*time.Hour)))

		require.NotNil(t, fake.detailReq)
		assert.Equal(t, requests.Integer("8089870"), fake.detailReq.CertId)
		// CertFilter=false：值为 true 时云侧不返回 Cert/Key 等内容字段（官方文档语义），
		// 发现导入的 PEM 材料通道依赖 Cert 字段返回。
		assert.Equal(t, requests.NewBoolean(false), fake.detailReq.CertFilter,
			"CertFilter 必须为 false，否则真实云侧响应恒无 PEM（活体验证回归：21613118-cn-hangzhou）")
	})

	t.Run("无PEM时回退CAS字段", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		detail := cas.CreateGetUserCertificateDetailResponse()
		detail.Fingerprint = "AB:CD:EF:01"
		detail.EndDate = "2027-01-02"
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailResp: detail}, nil
		}

		info, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, "abcdef01", info.Fingerprint)
		assert.Equal(t, 2027, info.NotAfter.UTC().Year())
	})

	t.Run("带地域后缀的证书ID自动归一化", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCasClient{}
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) { return fake, nil }

		_, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870-cn-hangzhou")
		require.NoError(t, err)
		require.NotNil(t, fake.detailReq)
		assert.Equal(t, requests.Integer("8089870"), fake.detailReq.CertId)
	})

	t.Run("证书不存在返回Exists=false", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailErr: errors.New("SDK.ServerError Message: The certificate does not exist. ErrorCode: Certificate.NotFind")}, nil
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "1")
		require.NoError(t, err)
		assert.False(t, info.Exists)
		assert.Empty(t, info.Fingerprint)
	})

	t.Run("404的ServerError归一化为不存在", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		notFound := sdkerrors.NewServerError(404, `{"Code":"NotFound"}`, "")
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailErr: notFound}, nil
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "1")
		require.NoError(t, err)
		assert.False(t, info.Exists)
	})

	t.Run("非404且非NotFound语义的ServerError透传", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		denied := sdkerrors.NewServerError(403, `{"Code":"Forbidden.RAM"}`, "")
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailErr: denied}, nil
		}
		_, err := adapter.GetCert(context.Background(), testCertCreds(), "1")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("PEM内容非法时回退CAS字段", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		detail := cas.CreateGetUserCertificateDetailResponse()
		detail.Cert = "not a pem"
		detail.Fingerprint = "AA:BB"
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailResp: detail}, nil
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Equal(t, "aabb", info.Fingerprint)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailErr: errFakeThrottling}, nil
		}
		_, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("非法证书ID显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.GetCert(context.Background(), testCertCreds(), "not-a-number")
		require.Error(t, err)
	})

	t.Run("返回净化fullchain序列_私钥块被丢弃_叶在前", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		rawBundle, wantChain, leafFingerprint := genTestCertChainPEM(t)
		detail := cas.CreateGetUserCertificateDetailResponse()
		detail.Cert = rawBundle
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{detailResp: detail}, nil
		}

		info, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		// 内容级断言：不含 PRIVATE KEY、含且仅含 CERTIFICATE 块（叶在前）
		assert.Equal(t, wantChain, info.CertChainPEM)
		assert.NotContains(t, info.CertChainPEM, "PRIVATE KEY")
		assert.True(t, strings.HasPrefix(info.CertChainPEM, "-----BEGIN CERTIFICATE-----"))
		// 指纹取净化序列首块（叶证书）
		assert.Equal(t, leafFingerprint, info.Fingerprint)
	})

	t.Run("无PEM时CertChainPEM为空", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{}, nil // 缺省 fake 仅含指纹/日期字段
		}
		info, err := adapter.GetCert(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
		assert.True(t, info.Exists)
		assert.Empty(t, info.CertChainPEM)
	})
}

// ==================== CleanupOrphan ====================

func TestCertAdapterCleanupOrphan(t *testing.T) {
	t.Run("成功清理", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		fake := &fakeCasClient{}
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) { return fake, nil }

		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
		require.Len(t, fake.deleteReqs, 1)
		assert.Equal(t, requests.Integer("8089870"), fake.deleteReqs[0].CertId)
	})

	t.Run("清理已不存在的证书视为幂等成功", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{deleteErr: errors.New("SDK.ServerError Message: cert not exist. ErrorCode: NotFound")}, nil
		}
		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "8089870")
		require.NoError(t, err)
	})

	t.Run("限流错误映射哨兵", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		adapter.newCasClient = func(creds *domain.CloudAccount) (casCertAPI, error) {
			return &fakeCasClient{deleteErr: errFakeThrottling}, nil
		}
		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "8089870")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCloudRateLimited)
	})

	t.Run("非法证书ID显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		err := adapter.CleanupOrphan(context.Background(), testCertCreds(), "")
		require.Error(t, err)
	})
}

// ==================== BindResource 分发 ====================

func TestCertAdapterBindResourceDispatch(t *testing.T) {
	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		err := adapter.BindResource(context.Background(), testCertCreds(), "clb", "res-1", "8089870")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})
}

// ==================== ListReferences 分发 ====================

func TestCertAdapterListReferencesDispatch(t *testing.T) {
	t.Run("不支持的产品显式报错", func(t *testing.T) {
		adapter := newTestCertAdapter(t)
		_, err := adapter.ListReferences(context.Background(), testCertCreds(), "oss")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCertProductNotSupported)
	})
}

// ==================== 辅助函数 ====================

func TestParseCASCertID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "纯数字", in: "8089870", want: 8089870},
		{name: "带地域后缀", in: "8089870-cn-hangzhou", want: 8089870},
		{name: "国际站后缀", in: "8089870-ap-southeast-1", want: 8089870},
		{name: "空串", in: "", wantErr: true},
		{name: "非数字", in: "abc", wantErr: true},
		{name: "非正数", in: "0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCASCertID(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCasRegion(t *testing.T) {
	assert.Equal(t, "cn-hangzhou", casRegion(""))
	assert.Equal(t, "cn-hangzhou", casRegion("cn-shanghai"))
	assert.Equal(t, "cn-hangzhou", casRegion("cn-hangzhou"))
	assert.Equal(t, "ap-southeast-1", casRegion("ap-southeast-1"))
}

func TestParseCloudCertTime(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		year  int
		valid bool
	}{
		{name: "RFC3339", in: "2027-01-02T08:00:00Z", year: 2027, valid: true},
		{name: "日期", in: "2027-01-02", year: 2027, valid: true},
		{name: "空格时间", in: "2027-01-02 08:00:00", year: 2027, valid: true},
		{name: "空串", in: "", valid: false},
		{name: "非法", in: "not-a-date", valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCloudCertTime(tt.in)
			if !tt.valid {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.year, got.Year())
		})
	}
}

// ==================== 真实客户端工厂离线构建 ====================

func TestCertAdapterRealClientFactories(t *testing.T) {
	// SDK 客户端构建不发起网络请求，可离线验证工厂与真实客户端满足接口签名
	creds := testCertCreds()
	adapter := NewCertAdapter(elog.DefaultLogger)
	t.Cleanup(adapter.Stop)

	casClient, err := adapter.newCasClient(creds)
	require.NoError(t, err)
	assert.NotNil(t, casClient)

	cdnClient, err := adapter.newCdnClient(creds)
	require.NoError(t, err)
	assert.NotNil(t, cdnClient)

	dcdnClient, err := adapter.newDcdnClient(creds)
	require.NoError(t, err)
	assert.NotNil(t, dcdnClient)

	albClient, err := adapter.newAlbClient(creds, "cn-hangzhou")
	require.NoError(t, err)
	assert.NotNil(t, albClient)

	nlbClient, err := adapter.newNlbClient(creds, "cn-hangzhou")
	require.NoError(t, err)
	assert.NotNil(t, nlbClient)

	wafCaller, err := adapter.newWafCaller(creds, "cn-hangzhou")
	require.NoError(t, err)
	assert.NotNil(t, wafCaller)
}
