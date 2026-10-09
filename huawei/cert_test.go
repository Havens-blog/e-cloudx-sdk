package huawei

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
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	cdnmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cdn/v2/model"
	elbmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	scmmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/scm/v3/model"
	wafmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/waf/v1/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 测试公共设施 ====================
// 命名与 cert_discovery_test.go 差异化（同包测试，禁止撞名）；
// fake 客户端均为单地域实例，region 寻址由测试内工厂闭包承载。

func certTestStrPtr(v string) *string { return &v }

// certTestLeafPEM 生成自签名测试证书（PEM 形态），返回 PEM 与解析后的叶证书
// （GetCert SHA-256 对齐口径断言用：指纹=叶 DER SHA256，NotAfter=叶 NotAfter）。
func certTestLeafPEM(t *testing.T, cn string) (string, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	notAfter := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return pemStr, leaf
}

// certTestHex 指纹断言小工具（叶 DER SHA256 hex）。
func certTestHex(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:])
}

// fakeScmDeployClient SCM 部署面 fake（Show/Import/Export/Delete 四方法）。
type fakeScmDeployClient struct {
	showResp   *scmmodel.ShowCertificateResponse
	showErr    error
	importResp *scmmodel.ImportCertificateResponse
	importErr  error
	exportResp *scmmodel.ExportCertificateResponse
	exportErr  error
	deleteErr  error

	importBodies []*scmmodel.ImportCertificateRequestBody
	exportIDs    []string
	deletedIDs   []string
}

func (f *fakeScmDeployClient) ShowCertificate(request *scmmodel.ShowCertificateRequest) (*scmmodel.ShowCertificateResponse, error) {
	return f.showResp, f.showErr
}

func (f *fakeScmDeployClient) ImportCertificate(request *scmmodel.ImportCertificateRequest) (*scmmodel.ImportCertificateResponse, error) {
	if f.importErr != nil {
		return nil, f.importErr
	}
	f.importBodies = append(f.importBodies, request.Body)
	return f.importResp, nil
}

func (f *fakeScmDeployClient) ExportCertificate(request *scmmodel.ExportCertificateRequest) (*scmmodel.ExportCertificateResponse, error) {
	f.exportIDs = append(f.exportIDs, request.CertificateId)
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return f.exportResp, nil
}

func (f *fakeScmDeployClient) DeleteCertificate(request *scmmodel.DeleteCertificateRequest) (*scmmodel.DeleteCertificateResponse, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, request.CertificateId)
	return &scmmodel.DeleteCertificateResponse{}, nil
}

// fakeCdnBindClient CDN 绑定面 fake。
type fakeCdnBindClient struct {
	err error

	requests []*cdnmodel.UpdateDomainMultiCertificatesRequestBodyContent
}

func (f *fakeCdnBindClient) UpdateDomainMultiCertificates(request *cdnmodel.UpdateDomainMultiCertificatesRequest) (*cdnmodel.UpdateDomainMultiCertificatesResponse, error) {
	if request.Body != nil {
		f.requests = append(f.requests, request.Body.Https)
	}
	if f.err != nil {
		return nil, f.err
	}
	return &cdnmodel.UpdateDomainMultiCertificatesResponse{}, nil
}

// fakeWafBindClient WAF 绑定面 fake（单地域实例：返回本域防护域名清单，记录 UpdateHost）。
type fakeWafBindClient struct {
	hosts []wafmodel.CloudWafHostItem
	err   error

	updateErr error

	updateRequests []*wafmodel.UpdateHostRequestBody
	updateHostIDs  []string
}

func (f *fakeWafBindClient) ListHost(request *wafmodel.ListHostRequest) (*wafmodel.ListHostResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	// 镜像真实 API 语义：hostname 过滤在服务端先于分页（分页遍历过滤后结果集）；
	// 过滤取前缀超集（真实过滤语义未实网确认），精确匹配由适配层兜底
	filtered := []wafmodel.CloudWafHostItem{}
	for _, h := range f.hosts {
		if request.Hostname != nil && !strings.HasPrefix(derefString(h.Hostname), *request.Hostname) {
			continue
		}
		filtered = append(filtered, h)
	}
	size := int32(50)
	if request.Pagesize != nil {
		size = *request.Pagesize
	}
	page := int32(1)
	if request.Page != nil {
		page = *request.Page
	}
	start := (page - 1) * size
	var items []wafmodel.CloudWafHostItem
	for i := start; i < int32(len(filtered)) && i < start+size; i++ {
		items = append(items, filtered[i])
	}
	if items == nil {
		items = []wafmodel.CloudWafHostItem{}
	}
	return &wafmodel.ListHostResponse{Items: &items}, nil
}

func (f *fakeWafBindClient) UpdateHost(request *wafmodel.UpdateHostRequest) (*wafmodel.UpdateHostResponse, error) {
	f.updateRequests = append(f.updateRequests, request.Body)
	f.updateHostIDs = append(f.updateHostIDs, request.InstanceId)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &wafmodel.UpdateHostResponse{}, nil
}

// fakeElbBindClient ELB 绑定面 fake（单地域实例：listener=nil 视为该地域 404 未命中；
// err500=true 时返回非存在语义错误）。
type fakeElbBindClient struct {
	listener *elbmodel.Listener
	err500   bool

	updateErr error

	updateListenerIDs []string
	updateOptions     []*elbmodel.UpdateListenerOption
}

func (f *fakeElbBindClient) ShowListener(request *elbmodel.ShowListenerRequest) (*elbmodel.ShowListenerResponse, error) {
	if f.err500 {
		return nil, &sdkerr.ServiceResponseError{StatusCode: 500, ErrorCode: "ELB.5000", ErrorMessage: "internal error"}
	}
	if f.listener == nil || f.listener.Id != request.ListenerId {
		return nil, &sdkerr.ServiceResponseError{StatusCode: 404, ErrorCode: "ELB.0001", ErrorMessage: "listener not found"}
	}
	return &elbmodel.ShowListenerResponse{Listener: f.listener}, nil
}

func (f *fakeElbBindClient) UpdateListener(request *elbmodel.UpdateListenerRequest) (*elbmodel.UpdateListenerResponse, error) {
	f.updateListenerIDs = append(f.updateListenerIDs, request.ListenerId)
	if request.Body != nil {
		f.updateOptions = append(f.updateOptions, request.Body.Listener)
	}
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &elbmodel.UpdateListenerResponse{}, nil
}

// newTestCertAdapter 构造可注入 fake SDK 客户端的完整证书适配器（限流器关闭避免排队）。
func newTestCertAdapter(t *testing.T) *CertAdapter {
	t.Helper()
	a := NewCertAdapter(elog.DefaultLogger)
	a.rateLimiter = nil
	return a
}

// ==================== UploadCert（两段式第一段） ====================

func TestCertAdapterUploadCert(t *testing.T) {
	scm := &fakeScmDeployClient{importResp: &scmmodel.ImportCertificateResponse{CertificateId: certTestStrPtr("scm-cert-9001")}}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	id, err := a.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "ecam-ab12-1765432100-0a1b", "cert-pem", "key-pem")
	require.NoError(t, err)
	assert.Equal(t, "scm-cert-9001", id)

	require.Len(t, scm.importBodies, 1)
	body := scm.importBodies[0]
	require.NotNil(t, body)
	assert.Equal(t, "ecam-ab12-1765432100-0a1b", body.Name)
	assert.Equal(t, "cert-pem", body.Certificate)
	assert.Equal(t, "key-pem", body.PrivateKey)
	// 与腾讯 Repeatable=true 同口径：每次更换生成独立云证书副本（允许内容重复）
	require.NotNil(t, body.DuplicateCheck)
	assert.False(t, *body.DuplicateCheck)
}

func TestCertAdapterUploadCertValidation(t *testing.T) {
	scm := &fakeScmDeployClient{importResp: &scmmodel.ImportCertificateResponse{CertificateId: certTestStrPtr("x")}}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	// 未支持产品显式报错
	_, err := a.UploadCert(context.Background(), certTestCreds(), "dcdn", "n", "c", "k")
	assert.ErrorIs(t, err, ErrCertProductNotSupported)
	// 材料缺失显式拒绝
	for _, tc := range []struct{ name, cert, key string }{{"", "c", "k"}, {"n", "", "k"}, {"n", "c", ""}} {
		_, err = a.UploadCert(context.Background(), certTestCreds(), CertProductCDN, tc.name, tc.cert, tc.key)
		assert.Error(t, err, "name=%q cert=%q key=%q 应拒绝", tc.name, tc.cert, tc.key)
	}
	// 凭证缺失
	_, err = a.UploadCert(context.Background(), nil, CertProductCDN, "n", "c", "k")
	assert.Error(t, err)
	assert.Empty(t, scm.importBodies, "校验失败不发起云调用")
}

func TestCertAdapterUploadCertThrottled(t *testing.T) {
	scm := &fakeScmDeployClient{importErr: errCertThrottled}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	_, err := a.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "n", "c", "k")
	assert.ErrorIs(t, err, ErrCloudRateLimited, "限流错误映射哨兵（部署器退避重试依据）")
}

func TestCertAdapterUploadCertEmptyID(t *testing.T) {
	scm := &fakeScmDeployClient{importResp: &scmmodel.ImportCertificateResponse{}}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	_, err := a.UploadCert(context.Background(), certTestCreds(), CertProductCDN, "n", "c", "k")
	assert.ErrorContains(t, err, "empty cloud cert id")
}

// ==================== BindResource（两段式第二段，按产品路由） ====================

func TestCertAdapterBindCDN(t *testing.T) {
	scm := &fakeScmDeployClient{showResp: &scmmodel.ShowCertificateResponse{Name: certTestStrPtr("ecam-ab12-1765432100-0a1b")}}
	cdn := &fakeCdnBindClient{}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a.newCdnBindClient = func(*domain.CloudAccount) (cdnBindCertAPI, error) { return cdn, nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductCDN, "www.example.com", "scm-cert-9001")
	require.NoError(t, err)

	// 绑定前经 SCM 解析证书名（CDN 绑定必填 cert_name + scm_certificate_id）
	require.Len(t, cdn.requests, 1)
	https := cdn.requests[0]
	require.NotNil(t, https)
	assert.Equal(t, "www.example.com", https.DomainName)
	assert.Equal(t, int32(1), https.HttpsSwitch)
	require.NotNil(t, https.CertificateType)
	assert.Equal(t, int32(2), *https.CertificateType, "certificate_type=2 为 SCM 托管证书")
	require.NotNil(t, https.ScmCertificateId)
	assert.Equal(t, "scm-cert-9001", *https.ScmCertificateId)
	require.NotNil(t, https.CertName)
	assert.Equal(t, "ecam-ab12-1765432100-0a1b", *https.CertName)
}

func TestCertAdapterBindCDNSCMCertNotFound(t *testing.T) {
	scm := &fakeScmDeployClient{showErr: errCertNotFound}
	cdn := &fakeCdnBindClient{}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a.newCdnBindClient = func(*domain.CloudAccount) (cdnBindCertAPI, error) { return cdn, nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductCDN, "www.example.com", "ghost-cert")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "ghost-cert")
	assert.Empty(t, cdn.requests, "SCM 解析失败不触达 CDN 绑定")
}

func TestCertAdapterBindWAF(t *testing.T) {
	scm := &fakeScmDeployClient{showResp: &scmmodel.ShowCertificateResponse{Name: certTestStrPtr("ecam-ab12-1765432100-0a1b")}}
	wafNorth := &fakeWafBindClient{hosts: []wafmodel.CloudWafHostItem{
		{Id: certTestStrPtr("host-decoy"), Hostname: certTestStrPtr("api.example.com")},
	}}
	wafEast := &fakeWafBindClient{hosts: []wafmodel.CloudWafHostItem{
		{Id: certTestStrPtr("host-1"), Hostname: certTestStrPtr("www.example.com")},
	}}
	wafClients := map[string]wafBindCertAPI{"cn-north-4": wafNorth, "cn-east-3": wafEast}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) { return wafClients[region], nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductWAF, "www.example.com", "scm-cert-9001")
	require.NoError(t, err)

	require.Len(t, wafEast.updateRequests, 1)
	body := wafEast.updateRequests[0]
	require.NotNil(t, body)
	assert.Equal(t, "host-1", wafEast.updateHostIDs[0], "按 hostname 过滤+精确匹配定位防护域名实例 ID")
	assert.Equal(t, "scm-cert-9001", derefString(body.Certificateid))
	assert.Equal(t, "ecam-ab12-1765432100-0a1b", derefString(body.Certificatename), "WAF UpdateHost HTTPS 域名必填证书名")
	assert.Empty(t, wafNorth.updateRequests, "未命中地域不产生更新调用")
}

func TestCertAdapterBindWAFHostNotFound(t *testing.T) {
	scm := &fakeScmDeployClient{showResp: &scmmodel.ShowCertificateResponse{Name: certTestStrPtr("n")}}
	wafNorth := &fakeWafBindClient{}
	wafEast := &fakeWafBindClient{}
	wafClients := map[string]wafBindCertAPI{"cn-north-4": wafNorth, "cn-east-3": wafEast}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) { return wafClients[region], nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductWAF, "missing.example.com", "scm-cert-9001")
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, wafNorth.updateRequests)
	assert.Empty(t, wafEast.updateRequests)
}

func TestCertAdapterBindELB(t *testing.T) {
	elbNorth := &fakeElbBindClient{}
	elbEast := &fakeElbBindClient{listener: &elbmodel.Listener{
		Id:                     "lsn-1",
		Protocol:               "TERMINATED_HTTPS",
		DefaultTlsContainerRef: "old-cert-id",
		SniContainerRefs:       []string{"sni-1", "sni-2"},
	}}
	elbClients := map[string]elbBindCertAPI{"cn-north-4": elbNorth, "cn-east-3": elbEast}
	a := newTestCertAdapter(t)
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return elbClients[region], nil }

	// 复合 resourceID "{LoadBalancerId}/{ListenerId}"（发现侧形态）
	err := a.BindResource(context.Background(), certTestCreds(), CertProductALB, "lb-1/lsn-1", "new-cert-id")
	require.NoError(t, err)

	require.Len(t, elbEast.updateListenerIDs, 1)
	assert.Equal(t, "lsn-1", elbEast.updateListenerIDs[0])
	opt := elbEast.updateOptions[0]
	require.NotNil(t, opt)
	require.NotNil(t, opt.DefaultTlsContainerRef)
	assert.Equal(t, "new-cert-id", *opt.DefaultTlsContainerRef, "新证书置默认位")
	require.NotNil(t, opt.SniContainerRefs)
	assert.Equal(t, []string{"sni-1", "sni-2"}, *opt.SniContainerRefs, "SNI 扩展证书保留")
	assert.Empty(t, elbNorth.updateListenerIDs, "未命中地域不产生更新调用")
}

func TestCertAdapterBindELBSNIOverlapRemoved(t *testing.T) {
	elb := &fakeElbBindClient{listener: &elbmodel.Listener{
		Id:                     "lsn-2",
		Protocol:               "TLS",
		DefaultTlsContainerRef: "old-cert-id",
		SniContainerRefs:       []string{"new-cert-id", "sni-1"},
	}}
	a := newTestCertAdapter(t)
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return elb, nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductNLB, "lb-1/lsn-2", "new-cert-id")
	require.NoError(t, err)

	require.Len(t, elb.updateOptions, 1)
	assert.Equal(t, []string{"sni-1"}, derefStrSlice(elb.updateOptions[0].SniContainerRefs),
		"新证书从 SNI 保留清单剔除（已在默认位，避免重复引用）")
}

func TestCertAdapterBindELBNonTLSProtocolRejected(t *testing.T) {
	elb := &fakeElbBindClient{listener: &elbmodel.Listener{Id: "lsn-3", Protocol: "HTTP"}}
	a := newTestCertAdapter(t)
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return elb, nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductALB, "lb-1/lsn-3", "new-cert-id")
	assert.ErrorContains(t, err, "no server certificate")
	assert.Empty(t, elb.updateListenerIDs)
}

func TestCertAdapterBindELBNotFoundAcrossRegions(t *testing.T) {
	elbNorth := &fakeElbBindClient{}
	elbEast := &fakeElbBindClient{}
	elbClients := map[string]elbBindCertAPI{"cn-north-4": elbNorth, "cn-east-3": elbEast}
	a := newTestCertAdapter(t)
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return elbClients[region], nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductALB, "lb-1/lsn-missing", "new-cert-id")
	assert.ErrorContains(t, err, "not found")
}

func TestCertAdapterBindUnsupportedProduct(t *testing.T) {
	a := newTestCertAdapter(t)
	err := a.BindResource(context.Background(), certTestCreds(), "clb", "x", "y")
	assert.ErrorIs(t, err, ErrCertProductNotSupported)
}

// 绑定输入校验矩阵：nil 凭证 / 空 resourceID / 空云证书 ID 显式拒绝且不触达产品客户端。
func TestCertAdapterBindValidationMatrix(t *testing.T) {
	cdn := &fakeCdnBindClient{}
	waf := &fakeWafBindClient{}
	elb := &fakeElbBindClient{listener: &elbmodel.Listener{Id: "lsn-1", Protocol: "HTTPS"}}
	a := newTestCertAdapter(t)
	a.newCdnBindClient = func(*domain.CloudAccount) (cdnBindCertAPI, error) { return cdn, nil }
	a.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) { return waf, nil }
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return elb, nil }
	ctx := context.Background()
	creds := certTestCreds()

	assert.Error(t, a.BindResource(ctx, nil, CertProductCDN, "www.example.com", "scm-1"))
	assert.Error(t, a.BindResource(ctx, creds, CertProductCDN, "", "scm-1"))
	assert.Error(t, a.BindResource(ctx, creds, CertProductCDN, "www.example.com", " "))
	assert.Error(t, a.BindResource(ctx, nil, CertProductWAF, "www.example.com", "scm-1"))
	assert.Error(t, a.BindResource(ctx, creds, CertProductWAF, "", "scm-1"))
	assert.Error(t, a.BindResource(ctx, creds, CertProductWAF, "www.example.com", " "))
	assert.Error(t, a.BindResource(ctx, nil, CertProductALB, "lb-1/lsn-1", "scm-1"))
	assert.Error(t, a.BindResource(ctx, creds, CertProductALB, "lb-1/lsn-1", " "))
	// 纯 "/" 分隔但两侧为空：解析不出监听器 ID → 显式拒绝
	assert.Error(t, a.BindResource(ctx, creds, CertProductALB, "/", "scm-1"))
	assert.Empty(t, cdn.requests)
	assert.Empty(t, waf.updateRequests)
	assert.Empty(t, elb.updateListenerIDs)
}

// ELB 纯监听 ID 形态（存量兼容：无 "{LoadBalancerId}/" 前缀）。
func TestCertAdapterBindELBBareListenerID(t *testing.T) {
	elb := &fakeElbBindClient{listener: &elbmodel.Listener{
		Id: "lsn-bare", Protocol: "HTTPS", DefaultTlsContainerRef: "old-cert-id",
	}}
	a := newTestCertAdapter(t)
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return elb, nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductALB, "lsn-bare", "new-cert-id")
	require.NoError(t, err)
	assert.Equal(t, []string{"lsn-bare"}, elb.updateListenerIDs, "纯监听 ID 直接寻址")
}

// WAF 绑定辅助失败路径：SCM 证书名为空 / SCM 客户端工厂失败 / ListHost 出错。
func TestCertAdapterBindWAFResolutionFailures(t *testing.T) {
	ctx := context.Background()
	creds := certTestCreds()

	// SCM 详情存在但证书名为空 → 显式失败（certificatename 必填无法满足）
	scmEmptyName := &fakeScmDeployClient{showResp: &scmmodel.ShowCertificateResponse{}}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scmEmptyName, nil }
	a.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) {
		return &fakeWafBindClient{}, nil
	}
	err := a.BindResource(ctx, creds, CertProductWAF, "www.example.com", "scm-1")
	assert.ErrorContains(t, err, "empty name")

	// WAF 客户端工厂失败
	scm := &fakeScmDeployClient{showResp: &scmmodel.ShowCertificateResponse{Name: certTestStrPtr("n")}}
	a2 := newTestCertAdapter(t)
	a2.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a2.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) {
		return nil, errors.New("region build failed")
	}
	err = a2.BindResource(ctx, creds, CertProductWAF, "www.example.com", "scm-1")
	assert.ErrorContains(t, err, "region build failed")

	// ListHost 出错 → 包装透传
	waf := &fakeWafBindClient{err: errCertThrottled}
	a3 := newTestCertAdapter(t)
	a3.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a3.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) { return waf, nil }
	err = a3.BindResource(ctx, creds, CertProductWAF, "www.example.com", "scm-1")
	assert.ErrorIs(t, err, ErrCloudRateLimited)
}

// WAF 跨页定位防护域名（hostname 过滤后首页满页无命中 → 翻页命中）。
func TestCertAdapterBindWAFHostPagination(t *testing.T) {
	scm := &fakeScmDeployClient{showResp: &scmmodel.ShowCertificateResponse{Name: certTestStrPtr("n")}}
	waf := &fakeWafBindClient{hosts: []wafmodel.CloudWafHostItem{
		{Id: certTestStrPtr("host-p1"), Hostname: certTestStrPtr("www.example.com.a")},
		{Id: certTestStrPtr("host-p2"), Hostname: certTestStrPtr("www.example.com.b")},
		{Id: certTestStrPtr("host-p3"), Hostname: certTestStrPtr("www.example.com")},
	}}
	a := newTestCertAdapter(t)
	a.listPageSize = 2
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	a.newWafBindClient = func(_ *domain.CloudAccount, region string) (wafBindCertAPI, error) { return waf, nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductWAF, "www.example.com", "scm-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"host-p3"}, waf.updateHostIDs, "首页满页精确匹配无命中 → 翻页第二页命中")
}

// ELB 定位非 404 错误：立即失败不跨地域续查。
func TestCertAdapterBindELBUnexpectedError(t *testing.T) {
	elbFail := &fakeElbBindClient{err500: true}
	elbEast := &fakeElbBindClient{listener: &elbmodel.Listener{Id: "lsn-1", Protocol: "HTTPS"}}
	clients := map[string]elbBindCertAPI{"cn-north-4": elbFail, "cn-east-3": elbEast}
	a := newTestCertAdapter(t)
	a.newElbBindClient = func(_ *domain.CloudAccount, region string) (elbBindCertAPI, error) { return clients[region], nil }

	err := a.BindResource(context.Background(), certTestCreds(), CertProductALB, "lb-1/lsn-1", "new-cert-id")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrCloudRateLimited)
	assert.Empty(t, elbEast.updateListenerIDs, "非存在语义错误不跨地域续查")
}

// GetCert 导出材料异常路径：空材料 / 非 PEM 内容 → 保留 ShowCertificate SHA-1 指纹。
func TestCertAdapterGetCertExportMalformed(t *testing.T) {
	emptyResp := &fakeScmDeployClient{
		showResp:   &scmmodel.ShowCertificateResponse{Fingerprint: certTestStrPtr("AA:BB")},
		exportResp: &scmmodel.ExportCertificateResponse{},
	}
	garbageResp := &fakeScmDeployClient{
		showResp:   &scmmodel.ShowCertificateResponse{Fingerprint: certTestStrPtr("AA:BB")},
		exportResp: &scmmodel.ExportCertificateResponse{Certificate: certTestStrPtr("not-a-pem")},
	}
	nonCertPEMResp := &fakeScmDeployClient{
		showResp:   &scmmodel.ShowCertificateResponse{Fingerprint: certTestStrPtr("AA:BB")},
		exportResp: &scmmodel.ExportCertificateResponse{Certificate: certTestStrPtr("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")},
	}
	for name, scm := range map[string]*fakeScmDeployClient{"empty": emptyResp, "garbage": garbageResp, "noncert": nonCertPEMResp} {
		a := newTestCertAdapter(t)
		a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
		info, err := a.GetCert(context.Background(), certTestCreds(), "scm-1")
		require.NoError(t, err, name)
		assert.True(t, info.Exists, name)
		assert.Equal(t, "aabb", info.Fingerprint, name+" 导出材料不可解析 → SHA-1 回退")
	}
}

// ==================== GetCert（回滚目标有效性校验） ====================

func TestCertAdapterGetCertSHA256Aligned(t *testing.T) {
	pemStr, leaf := certTestLeafPEM(t, "www.example.com")
	scm := &fakeScmDeployClient{
		showResp: &scmmodel.ShowCertificateResponse{
			Name:        certTestStrPtr("ecam-ab12-1765432100-0a1b"),
			Status:      certTestStrPtr("UPLOAD"),
			NotAfter:    certTestStrPtr("2027-01-02T08:00:00Z"),
			Fingerprint: certTestStrPtr("AA:BB:CC:DD"),
		},
		exportResp: &scmmodel.ExportCertificateResponse{Certificate: certTestStrPtr(pemStr)},
	}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	info, err := a.GetCert(context.Background(), certTestCreds(), "scm-cert-9001")
	require.NoError(t, err)
	assert.True(t, info.Exists)

	// SHA-256 对齐口径：叶 DER SHA256（64 hex），回滚前置指纹等值比对可用
	assert.Equal(t, certTestHex(leaf), info.Fingerprint)
	assert.Len(t, info.Fingerprint, 64)
	assert.WithinDuration(t, leaf.NotAfter, info.NotAfter, time.Second)

	// 导出请求带同一证书 ID
	assert.Equal(t, []string{"scm-cert-9001"}, scm.exportIDs)
}

func TestCertAdapterGetCertNotFound(t *testing.T) {
	scm := &fakeScmDeployClient{showErr: errCertNotFound}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	info, err := a.GetCert(context.Background(), certTestCreds(), "ghost-cert")
	require.NoError(t, err, "云侧已删除归一为 Exists=false 非错误")
	assert.False(t, info.Exists)
	assert.Empty(t, scm.exportIDs, "不存在证书不发起导出")
}

func TestCertAdapterGetCertExportFallbackToSHA1(t *testing.T) {
	scm := &fakeScmDeployClient{
		showResp: &scmmodel.ShowCertificateResponse{
			NotAfter:    certTestStrPtr("2027-01-02T08:00:00Z"),
			Fingerprint: certTestStrPtr("AA:BB:CC:DD"),
		},
		exportErr: errors.New("export forbidden"),
	}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	info, err := a.GetCert(context.Background(), certTestCreds(), "scm-cert-9001")
	require.NoError(t, err, "导出失败降级为 SHA-1 指纹（上层按无法复核处理），不阻断在库状态判定")
	assert.True(t, info.Exists)
	assert.Equal(t, "aabbccdd", info.Fingerprint, "SHA-1 冒号形态归一化（40hex，永不匹配台账 64hex 口径）")
	notAfter, ok := parseCloudCertTime("2027-01-02T08:00:00Z")
	require.True(t, ok)
	assert.WithinDuration(t, notAfter, info.NotAfter, time.Second)
}

func TestCertAdapterGetCertThrottled(t *testing.T) {
	scm := &fakeScmDeployClient{showErr: errCertThrottled}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	_, err := a.GetCert(context.Background(), certTestCreds(), "scm-cert-9001")
	assert.ErrorIs(t, err, ErrCloudRateLimited)
}

func TestCertAdapterGetCertValidation(t *testing.T) {
	scm := &fakeScmDeployClient{}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	_, err := a.GetCert(context.Background(), nil, "id")
	assert.Error(t, err)
	_, err = a.GetCert(context.Background(), certTestCreds(), " ")
	assert.Error(t, err)
	assert.Empty(t, scm.exportIDs)
}

// ==================== CleanupOrphan（孤儿清理，幂等） ====================

func TestCertAdapterCleanupOrphan(t *testing.T) {
	scm := &fakeScmDeployClient{}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }

	require.NoError(t, a.CleanupOrphan(context.Background(), certTestCreds(), "scm-cert-9001"))
	assert.Equal(t, []string{"scm-cert-9001"}, scm.deletedIDs)

	// 已删除幂等成功（清理队列重放安全）
	scm.deleteErr = errCertNotFound
	assert.NoError(t, a.CleanupOrphan(context.Background(), certTestCreds(), "scm-cert-9001"))

	// 其他错误透传
	scm.deleteErr = errCertThrottled
	assert.ErrorIs(t, a.CleanupOrphan(context.Background(), certTestCreds(), "scm-cert-9001"), ErrCloudRateLimited)
}

func TestCertAdapterCleanupOrphanValidation(t *testing.T) {
	scm := &fakeScmDeployClient{}
	a := newTestCertAdapter(t)
	a.newScmDeployClient = func(*domain.CloudAccount) (scmDeployCertAPI, error) { return scm, nil }
	assert.Error(t, a.CleanupOrphan(context.Background(), nil, "id"))
	assert.Error(t, a.CleanupOrphan(context.Background(), certTestCreds(), " "))
	assert.Empty(t, scm.deletedIDs)
}

// ==================== ListReferences（复用发现适配） ====================

func TestCertAdapterListReferencesDelegates(t *testing.T) {
	a := NewCertAdapter(elog.DefaultLogger)
	a.rateLimiter = nil
	a.listPageSize = 10
	a.newCdnClient = func(*domain.CloudAccount) (cdnCertAPI, error) {
		return &fakeCDNCertClient{pages: [][]cdnmodel.HttpsDetail{{
			{DomainName: certTestStrPtr("www.example.com"), CertName: certTestStrPtr("ecam-ab12-1765432100-0a1b"), HttpsStatus: certTestInt32(1)},
		}}}, nil
	}

	refs, err := a.ListReferences(context.Background(), certTestCreds(), CertProductCDN)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "www.example.com", refs[0].ResourceID)
	assert.Equal(t, "ecam-ab12-1765432100-0a1b", refs[0].ReferencedCloudCertID)
}

// ==================== 真实客户端工厂边界 ====================

func TestCertAdapterRealClientFactories(t *testing.T) {
	a := NewCertAdapter(elog.DefaultLogger)
	creds := certTestCreds()

	// 工厂注入点齐备（SCM/CDN/WAF/ELB 绑定面）
	assert.NotNil(t, a.newScmDeployClient)
	assert.NotNil(t, a.newCdnBindClient)
	// WAF/ELB 非法地域在 IAM 解析前快速失败（不触网）
	_, err := a.newWafBindClient(creds, "not-a-region")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WAF地域")
	_, err = a.newElbBindClient(creds, "not-a-region")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ELB地域")
}

// ==================== 小工具 ====================

func derefStrSlice(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}
