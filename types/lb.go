package types

// LBType 负载均衡类型
type LBType string

const (
	LBTypeSLB LBType = "slb" // 传统负载均衡 (Classic Load Balancer)
	LBTypeALB LBType = "alb" // 应用型负载均衡 (Application Load Balancer)
	LBTypeNLB LBType = "nlb" // 网络型负载均衡 (Network Load Balancer)
)

// LBInstance 负载均衡实例（通用格式）
type LBInstance struct {
	// 基本信息
	LoadBalancerID   string `json:"load_balancer_id"`   // 实例ID
	LoadBalancerName string `json:"load_balancer_name"` // 实例名称
	LoadBalancerType string `json:"load_balancer_type"` // 类型: slb/alb/nlb
	Status           string `json:"status"`             // 状态
	Region           string `json:"region"`             // 地域
	Zone             string `json:"zone"`               // 主可用区
	SlaveZone        string `json:"slave_zone"`         // 备可用区

	// 网络信息
	Address          string `json:"address"`            // VIP地址
	AddressType      string `json:"address_type"`       // 地址类型: internet/intranet
	AddressIPVersion string `json:"address_ip_version"` // IP版本: ipv4/ipv6/dualstack
	VPCID            string `json:"vpc_id"`             // VPC ID
	VPCName          string `json:"vpc_name"`           // VPC名称
	VSwitchID        string `json:"vswitch_id"`         // 交换机ID
	NetworkType      string `json:"network_type"`       // 网络类型: vpc/classic

	// 规格信息
	LoadBalancerSpec    string `json:"load_balancer_spec"`    // 规格 (SLB)
	LoadBalancerEdition string `json:"load_balancer_edition"` // 版本 (ALB: Basic/Standard/WAF)
	Bandwidth           int    `json:"bandwidth"`             // 带宽(Mbps)
	BandwidthPackageID  string `json:"bandwidth_package_id"`  // 带宽包ID

	// 监听器和后端信息
	ListenerCount      int               `json:"listener_count"`       // 监听器数量
	BackendServerCount int               `json:"backend_server_count"` // 后端服务器数量
	Listeners          []LBListener      `json:"listeners"`            // 监听器详情列表
	BackendServers     []LBBackendServer `json:"backend_servers"`      // 后端服务器详情列表

	// 计费信息
	ChargeType         string `json:"charge_type"`          // 付费类型: PrePaid/PostPaid
	InternetChargeType string `json:"internet_charge_type"` // 计费方式: PayByBandwidth/PayByTraffic
	CreationTime       string `json:"creation_time"`        // 创建时间
	ExpiredTime        string `json:"expired_time"`         // 过期时间

	// 项目/资源组信息
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ResourceGroupID string `json:"resource_group_id"`

	// 云账号信息
	CloudAccountID   int64  `json:"cloud_account_id"`
	CloudAccountName string `json:"cloud_account_name"`

	// 其他信息
	Tags        map[string]string `json:"tags"`
	Description string            `json:"description"`
	Provider    string            `json:"provider"` // 云厂商标识
}

// LBInstanceFilter LB实例过滤条件
type LBInstanceFilter struct {
	LoadBalancerIDs  []string          `json:"load_balancer_ids,omitempty"`
	LoadBalancerName string            `json:"load_balancer_name,omitempty"`
	LoadBalancerType string            `json:"load_balancer_type,omitempty"` // slb/alb/nlb
	Status           []string          `json:"status,omitempty"`
	AddressType      string            `json:"address_type,omitempty"` // internet/intranet
	VPCID            string            `json:"vpc_id,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
	PageNumber       int               `json:"page_number,omitempty"`
	PageSize         int               `json:"page_size,omitempty"`
}

// LBListener 负载均衡监听器
type LBListener struct {
	ListenerID       string `json:"listener_id"`
	ListenerPort     int    `json:"listener_port"`
	ListenerProtocol string `json:"listener_protocol"` // TCP/UDP/HTTP/HTTPS
	BackendPort      int    `json:"backend_port"`
	Status           string `json:"status"`
	Bandwidth        int    `json:"bandwidth"`
	Description      string `json:"description"`
}

// LBBackendServer 后端服务器
type LBBackendServer struct {
	ServerID    string `json:"server_id"`
	ServerName  string `json:"server_name"`
	InstanceID  string `json:"instance_id"` // 云主机实例ID
	IP          string `json:"ip"`          // 后端服务器IP地址
	Port        int    `json:"port"`
	Weight      int    `json:"weight"`
	Type        string `json:"type"` // ecs/eni/ip
	Status      string `json:"status"`
	Description string `json:"description"`
}
