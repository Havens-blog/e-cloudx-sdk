package types

// CloudProvider 云厂商类型
type CloudProvider string

const (
	ProviderAliyun  CloudProvider = "aliyun"
	ProviderAWS     CloudProvider = "aws"
	ProviderAzure   CloudProvider = "azure"
	ProviderHuawei  CloudProvider = "huawei"
	ProviderVolcano CloudProvider = "volcano"
	ProviderTencent CloudProvider = "tencent"
)

// Region 地域信息
type Region struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	LocalName   string `json:"local_name"`
	Description string `json:"description"`
}

// ECSInstance 云主机实例（通用格式）
type ECSInstance struct {
	// 基本信息
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	Status       string `json:"status"`
	Region       string `json:"region"`
	Zone         string `json:"zone"`

	// 配置信息
	InstanceType       string `json:"instance_type"`
	InstanceTypeFamily string `json:"instance_type_family"`
	CPU                int    `json:"cpu"`
	Memory             int    `json:"memory"` // MB
	OSType             string `json:"os_type"`
	OSName             string `json:"os_name"`

	// 镜像信息
	ImageID   string `json:"image_id"`
	ImageName string `json:"image_name"`

	// 网络信息
	PublicIP                string          `json:"public_ip"`
	PrivateIP               string          `json:"private_ip"`
	VPCID                   string          `json:"vpc_id"`
	VPCName                 string          `json:"vpc_name"`
	VSwitchID               string          `json:"vswitch_id"`
	VSwitchName             string          `json:"vswitch_name"`
	SecurityGroups          []SecurityGroup `json:"security_groups"`
	InternetMaxBandwidthIn  int             `json:"internet_max_bandwidth_in"`
	InternetMaxBandwidthOut int             `json:"internet_max_bandwidth_out"`

	// 存储信息
	SystemDisk SystemDisk `json:"system_disk"`
	DataDisks  []DataDisk `json:"data_disks"`

	// 计费信息
	ChargeType      string `json:"charge_type"`
	CreationTime    string `json:"creation_time"`
	ExpiredTime     string `json:"expired_time"`
	AutoRenew       bool   `json:"auto_renew"`
	AutoRenewPeriod int    `json:"auto_renew_period"`

	// 监控信息
	IoOptimized         string `json:"io_optimized"`
	NetworkType         string `json:"network_type"`
	InstanceNetworkType string `json:"instance_network_type"`

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
	HostName    string            `json:"host_name"`
	KeyPairName string            `json:"key_pair_name"`
}

// SecurityGroup 安全组信息
type SecurityGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SystemDisk 系统盘信息
type SystemDisk struct {
	DiskID       string `json:"disk_id"`
	Category     string `json:"category"`      // 磁盘类型: cloud_efficiency, cloud_ssd, cloud_essd 等
	Size         int    `json:"size"`          // GB
	Device       string `json:"device"`        // 设备名
	PerformLevel string `json:"perform_level"` // 性能等级 (ESSD)
}

// DataDisk 数据盘信息
type DataDisk struct {
	DiskID             string `json:"disk_id"`
	Category           string `json:"category"`
	Size               int    `json:"size"` // GB
	Device             string `json:"device"`
	PerformLevel       string `json:"perform_level"`
	Encrypted          bool   `json:"encrypted"`
	DeleteWithInstance bool   `json:"delete_with_instance"`
}
