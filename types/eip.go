package types

// EIPInstance 弹性公网IP实例（通用格式）
type EIPInstance struct {
	// 基本信息
	AllocationID string `json:"allocation_id"` // EIP实例ID
	Name         string `json:"name"`          // EIP名称
	Status       string `json:"status"`        // 状态
	Region       string `json:"region"`        // 地域
	Zone         string `json:"zone"`          // 可用区

	// IP信息
	IPAddress        string `json:"ip_address"`         // 公网IP地址
	PrivateIPAddress string `json:"private_ip_address"` // 私网IP地址（部分云支持）
	IPVersion        string `json:"ip_version"`         // IP版本: IPv4, IPv6

	// 带宽信息
	Bandwidth            int    `json:"bandwidth"`              // 带宽(Mbps)
	InternetChargeType   string `json:"internet_charge_type"`   // 计费方式: PayByBandwidth, PayByTraffic
	BandwidthPackageID   string `json:"bandwidth_package_id"`   // 共享带宽包ID
	BandwidthPackageName string `json:"bandwidth_package_name"` // 共享带宽包名称

	// 绑定资源信息
	InstanceID   string `json:"instance_id"`   // 绑定的实例ID
	InstanceType string `json:"instance_type"` // 绑定的实例类型: EcsInstance, SlbInstance, Nat, HaVip, NetworkInterface 等
	InstanceName string `json:"instance_name"` // 绑定的实例名称

	// 网络信息
	VPCID            string `json:"vpc_id"`            // VPC ID
	VSwitchID        string `json:"vswitch_id"`        // 交换机ID
	NetworkInterface string `json:"network_interface"` // 绑定的网卡ID
	ISP              string `json:"isp"`               // 线路类型: BGP, BGP_PRO, ChinaTelecom, ChinaUnicom, ChinaMobile
	Netmode          string `json:"netmode"`           // 网络类型: public, hybrid
	SegmentID        string `json:"segment_id"`        // 连续IP段ID
	PublicIPPool     string `json:"public_ip_pool"`    // 公网IP地址池ID
	ResourceGroupID  string `json:"resource_group_id"` // 资源组ID
	SecurityGroupID  string `json:"security_group_id"` // 安全组ID（部分云支持）

	// 计费信息
	ChargeType   string `json:"charge_type"`   // 付费类型: PrePaid, PostPaid
	CreationTime string `json:"creation_time"` // 创建时间
	ExpiredTime  string `json:"expired_time"`  // 过期时间

	// 项目/资源组信息
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags        map[string]string `json:"tags"`
	Description string            `json:"description"`
	Provider    string            `json:"provider"` // 云厂商标识
}

// EIPInstanceFilter EIP实例过滤条件
type EIPInstanceFilter struct {
	AllocationIDs    []string          `json:"allocation_ids,omitempty"`
	IPAddresses      []string          `json:"ip_addresses,omitempty"`
	Name             string            `json:"name,omitempty"`
	Status           []string          `json:"status,omitempty"`
	InstanceID       string            `json:"instance_id,omitempty"`       // 绑定的实例ID
	InstanceType     string            `json:"instance_type,omitempty"`     // 绑定的实例类型
	AssociatedOnly   bool              `json:"associated_only,omitempty"`   // 只返回已绑定的EIP
	UnassociatedOnly bool              `json:"unassociated_only,omitempty"` // 只返回未绑定的EIP
	VPCID            string            `json:"vpc_id,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
	PageNumber       int               `json:"page_number,omitempty"`
	PageSize         int               `json:"page_size,omitempty"`
}

// EIP 绑定资源类型常量
const (
	EIPInstanceTypeECS             = "EcsInstance"      // ECS实例
	EIPInstanceTypeSLB             = "SlbInstance"      // 负载均衡
	EIPInstanceTypeNAT             = "Nat"              // NAT网关
	EIPInstanceTypeHaVip           = "HaVip"            // 高可用虚拟IP
	EIPInstanceTypeENI             = "NetworkInterface" // 弹性网卡
	EIPInstanceTypeClusterIP       = "ClusterIp"        // 集群IP（K8s）
	EIPInstanceTypeGatewayEndpoint = "GatewayEndpoint"  // 网关终端节点
)
