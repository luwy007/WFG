package models

// NodeType 节点协议类型
type NodeType string

const (
	NodeTypeVMess       NodeType = "vmess"
	NodeTypeVLESS       NodeType = "vless"
	NodeTypeShadowsocks NodeType = "ss"
	NodeTypeTrojan      NodeType = "trojan"
)

// Node 代理节点
type Node struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Type           NodeType       `json:"type"`
	SubscriptionID string         `json:"subscription_id"`
	Latency        int            `json:"latency_ms"` // -1 = 未测试, 0 = 超时
	Raw            map[string]any `json:"raw"`        // 原始 Clash 格式字段
}

// VMess 节点配置（用于解析 vmess:// URI）
type VMess struct {
	Version  string `json:"v"`
	Name     string `json:"ps"`
	Address  string `json:"add"`
	Port     string `json:"port"`
	UUID     string `json:"id"`
	AltID    int    `json:"aid"`
	Security string `json:"scy"`
	Network  string `json:"net"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Path     string `json:"path"`
	TLS      string `json:"tls"`
	SNI      string `json:"sni"`
}

// AppConfig 应用全局配置（持久化到磁盘）
type AppConfig struct {
	MixedPort     int            `json:"mixed_port"`
	APIPort       int            `json:"api_port"`
	MihomoSecret  string         `json:"mihomo_secret"`
	Subscriptions []Subscription `json:"subscriptions"`
	SelectedNode  string         `json:"selected_node"`
	ProxyMode     ProxyMode      `json:"proxy_mode"`
	TunEnabled    bool           `json:"tun_enabled"`
	Rules         []Rule         `json:"rules"`
}

type ProxyMode string

const (
	ProxyModeRule   ProxyMode = "rule"
	ProxyModeGlobal ProxyMode = "global"
	ProxyModeDirect ProxyMode = "direct"
)

// Rule 分流规则
type Rule struct {
	Type    RuleType `json:"type"`
	Payload string   `json:"payload"`
	Policy  string   `json:"policy"` // PROXY / DIRECT / 节点组名
}

type RuleType string

const (
	RuleTypeDomainSuffix  RuleType = "DOMAIN-SUFFIX"
	RuleTypeDomainKeyword RuleType = "DOMAIN-KEYWORD"
	RuleTypeDomain        RuleType = "DOMAIN"
	RuleTypeGeoIP         RuleType = "GEOIP"
	RuleTypeGeoSite       RuleType = "GEOSITE"
	RuleTypeIPCIDR        RuleType = "IP-CIDR"
	RuleTypeDstPort       RuleType = "DST-PORT"
	RuleTypeMatch         RuleType = "MATCH"
)
