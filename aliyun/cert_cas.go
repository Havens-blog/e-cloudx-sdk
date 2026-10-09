package aliyun

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/cas"
	"github.com/gotomicro/ego/core/elog"
)

// CAS 证书库（SSL 证书服务「我的证书」）清单实现（cert-cas-library-scan）。
// 发现：ListUserCertificateOrder（ShowSize/CurrentPage 分页，CertificateId 为
// CAS 数字证书 ID）；仅只读发现语义——证书库条目无资源绑定概念，不参与
// UploadCert/BindResource/certSupportedProducts（不可部署）。
//
// 实测行为（2026-09-03 活体验证，实现处以注释固化）：
//   - OrderType 缺省恒返回 TotalCount=0——控制台「我的证书」默认不返回 Upload
//     类型，必须显式 OrderType="Upload"；
//   - SDK 的 ListCert 查的是证书仓库（另一个功能，恒 0），与 ListUserCertificateOrder
//     名字相近但语义不同，勿混用。

// casOrderTypeUpload ListUserCertificateOrder 的订单类型过滤值（上传到证书库的证书）。
// 缺省时 API 恒返回空集，必须显式传入（见上方实测行为注释）。
const casOrderTypeUpload = "Upload"

// listCASReferences 分页遍历 CAS 证书库（OrderType=Upload），每张证书产出一条
// 引用：resourceId=证书名称（Name）、referencedCloudCertId=数字证书 ID（CertificateId）。
// 发现预览清单由此 = 资源引用 ∪ 证书库全集；终止条件与 listCDNReferences 同口径
// （页未满 + TotalCount）。
func (a *CertAdapter) listCASReferences(ctx context.Context, creds *domain.CloudAccount) ([]CloudCertRef, error) {
	if creds == nil {
		return nil, fmt.Errorf("aliyun cas cert list: nil creds")
	}
	client, err := a.newCasClient(creds)
	if err != nil {
		return nil, err
	}

	accountKey := creds.Name
	var refs []CloudCertRef
	collected := 0
	currentPage := 1
	for {
		if err := a.waitRateLimit(ctx); err != nil {
			return nil, err
		}
		request := cas.CreateListUserCertificateOrderRequest()
		request.Scheme = "https"
		request.ShowSize = requests.NewInteger(a.certPageSize())
		request.CurrentPage = requests.NewInteger(currentPage)
		// 必须显式 Upload：缺省 OrderType 恒 TotalCount=0（见文件头实测注释）
		request.OrderType = casOrderTypeUpload

		response, err := client.ListUserCertificateOrder(request)
		if err != nil {
			return nil, wrapCertCloudErr(CertProductCAS, err)
		}
		entries := response.CertificateOrderList
		collected += len(entries)
		for _, entry := range entries {
			// 无数字证书 ID 的条目无法定位证书材料，不构成引用
			if entry.CertificateId <= 0 {
				continue
			}
			refs = append(refs, CloudCertRef{
				Cloud:                 "aliyun",
				Product:               CertProductCAS,
				ResourceID:            entry.Name,
				ReferencedCloudCertID: strconv.FormatInt(entry.CertificateId, 10),
				AccountKey:            accountKey,
			})
		}
		// 页未满或已达 TotalCount（TotalCount=0 视为未知，仅按页数据判定）均视为遍历完成
		if len(entries) < a.certPageSize() {
			break
		}
		if response.TotalCount > 0 && int64(collected) >= response.TotalCount {
			break
		}
		currentPage++
	}

	a.logger.Info("获取阿里云CAS证书库引用成功",
		elog.String("account", accountKey),
		elog.Int("count", len(refs)))
	return refs, nil
}
