package models

import "time"

// SubFormat 订阅内容格式
type SubFormat string

const (
	SubFormatClash  SubFormat = "clash"  // Clash YAML
	SubFormatBase64 SubFormat = "base64" // Base64 编码的 URI 列表
	SubFormatSSD    SubFormat = "ssd"    // SSD 格式（SS 专用）
)

// Subscription 机场订阅
type Subscription struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	Format          SubFormat `json:"format"`
	UpdatedAt       time.Time `json:"updated_at"`
	NodeCount       int       `json:"node_count"`
	UserInfo        *UserInfo `json:"user_info,omitempty"` // 流量信息（部分机场提供）
	AutoRefresh     bool      `json:"auto_refresh"`
	RefreshInterval int       `json:"refresh_interval"` // 自动刷新周期（分钟），默认60
	AutoRefreshFrom time.Time `json:"auto_refresh_from"`
	Nodes           []Node    `json:"nodes,omitempty"`
}

// SubLogEntry 订阅刷新日志条目
type SubLogEntry struct {
	Time      time.Time `json:"time"`
	SubID     string    `json:"sub_id"`
	SubName   string    `json:"sub_name"`
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	NodeCount int       `json:"node_count"`
	IPs       []string  `json:"ips"` // 解析出的节点IP列表
}

// UserInfo 机场用户流量信息（从 Subscription-Userinfo 响应头解析）
type UserInfo struct {
	Upload   int64 `json:"upload"`   // 已用上行（字节）
	Download int64 `json:"download"` // 已用下行（字节）
	Total    int64 `json:"total"`    // 总流量（字节）
	Expire   int64 `json:"expire"`   // 到期时间戳
}

// LatencyResult 节点延迟测试结果
type LatencyResult struct {
	NodeID  string `json:"node_id"`
	Latency int    `json:"latency_ms"` // 0 = 超时
	Error   string `json:"error,omitempty"`
}
