package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wfg/engine/models"
	"gopkg.in/yaml.v3"
)

const (
	defaultMixedPort  = 7890
	defaultAPIPort    = 9090
	defaultEnginePort = 19090 // 我们自己的 API 端口
)

var dataDirOverride string

// SetDataDir 覆盖应用数据目录。用于 root LaunchDaemon 继续读写登录用户的 ~/.wfg。
func SetDataDir(path string) {
	dataDirOverride = strings.TrimSpace(path)
}

// DataDir 返回应用数据目录
func DataDir() string {
	if dataDirOverride != "" {
		return dataDirOverride
	}
	if env := strings.TrimSpace(os.Getenv("WFG_DATA_DIR")); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".wfg")
}

// AppConfigPath 应用配置文件路径
func AppConfigPath() string {
	return filepath.Join(DataDir(), "app_config.json")
}

// MihomoConfigPath mihomo 运行配置路径
func MihomoConfigPath() string {
	return filepath.Join(DataDir(), "mihomo_config.yaml")
}

// MihomoBinPath mihomo 二进制路径
func MihomoBinPath() string {
	return filepath.Join(DataDir(), "mihomo")
}

// GeoDataDir geo 数据库目录
func GeoDataDir() string {
	return filepath.Join(DataDir(), "geo")
}

// LoadAppConfig 从磁盘加载应用配置，不存在则返回默认值
func LoadAppConfig() (*models.AppConfig, error) {
	data, err := os.ReadFile(AppConfigPath())
	if os.IsNotExist(err) {
		return defaultAppConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg models.AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	ensureDefaultDirectRules(&cfg)
	return &cfg, nil
}

// SaveAppConfig 将应用配置写入磁盘
func SaveAppConfig(cfg *models.AppConfig) error {
	ensureDefaultDirectRules(cfg)
	if err := os.MkdirAll(DataDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(AppConfigPath(), data, 0600)
}

func defaultAppConfig() *models.AppConfig {
	cfg := &models.AppConfig{
		MixedPort:    defaultMixedPort,
		APIPort:      defaultAPIPort,
		MihomoSecret: "",
		ProxyMode:    models.ProxyModeRule,
		Rules:        defaultRules(),
	}
	ensureDefaultDirectRules(cfg)
	return cfg
}

// defaultRules 默认分流规则（SSH/国内直连，其余走代理）
func defaultRules() []models.Rule {
	return []models.Rule{
		sshDirectRule(),
		{Type: models.RuleTypeGeoSite, Payload: "private", Policy: "DIRECT"},
		{Type: models.RuleTypeGeoSite, Payload: "cn", Policy: "DIRECT"},
		{Type: models.RuleTypeGeoIP, Payload: "private", Policy: "DIRECT"},
		{Type: models.RuleTypeGeoIP, Payload: "CN", Policy: "DIRECT"},
		{Type: models.RuleTypeMatch, Payload: "", Policy: "手动选择"},
	}
}

func sshDirectRule() models.Rule {
	return models.Rule{Type: models.RuleTypeDstPort, Payload: "22", Policy: "DIRECT"}
}

func directDNSServers() []string {
	return []string{
		"system",
		"https://doh.pub/dns-query#DIRECT",
		"https://dns.alidns.com/dns-query#DIRECT",
	}
}

func directFallbackDNSServers() []string {
	return []string{
		"https://cloudflare-dns.com/dns-query#DIRECT",
		"https://dns.google/dns-query#DIRECT",
		"tls://1.1.1.1:853#DIRECT",
	}
}

func ensureDefaultDirectRules(cfg *models.AppConfig) {
	if cfg == nil {
		return
	}

	hasSSHDirect := false
	rules := make([]models.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		if r.Type == models.RuleTypeDstPort && strings.TrimSpace(r.Payload) == "22" && r.Policy == "DIRECT" {
			hasSSHDirect = true
			continue
		}
		rules = append(rules, r)
	}
	if hasSSHDirect {
		cfg.Rules = append([]models.Rule{sshDirectRule()}, rules...)
		return
	}
	cfg.Rules = append([]models.Rule{sshDirectRule()}, cfg.Rules...)
}

// GenerateMihomoConfig 根据当前配置和节点列表生成 mihomo YAML 配置
func GenerateMihomoConfig(cfg *models.AppConfig, nodes []models.Node) error {
	if err := os.MkdirAll(DataDir(), 0755); err != nil {
		return err
	}

	miCfg := buildMihomoConfig(cfg, nodes)
	data, err := yaml.Marshal(miCfg)
	if err != nil {
		return fmt.Errorf("序列化 mihomo 配置失败: %w", err)
	}
	return os.WriteFile(MihomoConfigPath(), data, 0644)
}

// buildMihomoConfig 构建 mihomo 配置结构
func buildMihomoConfig(cfg *models.AppConfig, nodes []models.Node) map[string]any {
	ensureDefaultDirectRules(cfg)

	// 代理节点列表
	proxies := make([]map[string]any, 0, len(nodes))
	proxyNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		proxies = append(proxies, n.Raw)
		proxyNames = append(proxyNames, n.Name)
	}

	// 代理组（udp:true 让 QUIC/HTTP3 也能走代理，避免 YouTube 等站点卡顿回退）
	proxyGroups := []map[string]any{
		{
			"name":      "PROXY",
			"type":      "url-test",
			"proxies":   proxyNames,
			"url":       "http://www.gstatic.com/generate_204",
			"interval":  300,
			"tolerance": 50,
			"udp":       true,
		},
		{
			"name":    "手动选择",
			"type":    "select",
			"proxies": append([]string{"PROXY"}, proxyNames...),
			"udp":     true,
		},
	}

	// 分流规则（MATCH 规则强制使用"手动选择"组，兼容旧配置）
	subDirectRules := subscriptionDirectRules(cfg.Subscriptions)
	rules := make([]string, 0, len(cfg.Rules)+len(subDirectRules))
	for _, r := range cfg.Rules {
		if r.Type == models.RuleTypeMatch {
			rules = append(rules, subDirectRules...)
			rules = append(rules, "MATCH,手动选择")
		} else {
			rules = append(rules, fmt.Sprintf("%s,%s,%s", r.Type, r.Payload, r.Policy))
		}
	}

	result := map[string]any{
		"mixed-port":          cfg.MixedPort,
		"allow-lan":           false,
		"bind-address":        "127.0.0.1",
		"mode":                string(cfg.ProxyMode),
		"log-level":           "info",
		"external-controller": fmt.Sprintf("127.0.0.1:%d", cfg.APIPort),
		"secret":              cfg.MihomoSecret,
		"geodata-mode":        true,
		"geodata-loader":      "standard",
		// Happy Eyeballs 加强版：并发连接多个 IP 取最快，显著加速多 POP 海外站点
		"tcp-concurrent": true,
		"unified-delay":  true,
		// 嗅探 TLS ClientHello / HTTP Host，让硬编码 IP 的连接也能命中域名规则
		"sniffer": map[string]any{
			"enable":            true,
			"force-dns-mapping": true,
			"parse-pure-ip":     true,
			"sniff": map[string]any{
				"TLS":  map[string]any{"ports": []int{443, 8443}},
				"HTTP": map[string]any{"ports": []int{80, 8080, 8880}},
				"QUIC": map[string]any{"ports": []int{443}},
			},
			"skip-domain": []string{"Mijia Cloud", "+.push.apple.com"},
		},
		"geox-url": map[string]any{
			"geoip":   "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat",
			"geosite": "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
			"mmdb":    "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb",
		},
		"dns": map[string]any{
			"enable":        true,
			"listen":        "127.0.0.1:5354",
			"ipv6":          false,
			"enhanced-mode": "fake-ip",
			"fake-ip-range": "198.18.0.1/16",
			// 节点域名解析固定走直连出口，避免代理服务器 IP 解析被代理链路自身影响。
			"proxy-server-nameserver": directDNSServers(),
			// DIRECT 出口的域名解析也固定走直连，不跟随分流规则切到代理。
			"direct-nameserver":               directDNSServers(),
			"direct-nameserver-follow-policy": false,
			"fake-ip-filter": []string{
				"*.lan",
				"*.local",
				"localhost.ptlogin2.qq.com",
				"+.msftconnecttest.com",
				"+.msftncsi.com",
				"time.*.com",
				"time.*.apple.com",
			},
			// .local 是 mDNS/Bonjour 域名，只有系统 DNS 能解析
			"nameserver-policy": map[string]any{
				"*.local": "system",
			},
			// 所有 DNS 上游请求都指定 DIRECT，保证解析动作本身不走代理。
			"nameserver": directDNSServers(),
			"fallback":   directFallbackDNSServers(),
			"fallback-filter": map[string]any{
				"geoip":      true,
				"geoip-code": "CN",
				"domain": []string{
					"+.google.com",
					"+.youtube.com",
					"+.googlevideo.com",
					"+.ytimg.com",
					"+.ggpht.com",
					"+.githubusercontent.com",
				},
			},
		},
		"proxies":      proxies,
		"proxy-groups": proxyGroups,
		"rules":        rules,
	}

	// TUN 模式：创建 utun 虚拟网卡，接管所有流量（包括 UDP/ICMP）
	if cfg.TunEnabled {
		result["tun"] = map[string]any{
			"enable":                true,
			"stack":                 "mixed",
			"auto-route":            true,
			"auto-detect-interface": true,
			"dns-hijack":            []string{"any:53"},
			"strict-route":          false,
			"mtu":                   9000,
		}
	}
	return result
}

func subscriptionDirectRules(subs []models.Subscription) []string {
	seen := make(map[string]struct{})
	rules := make([]string, 0, len(subs))
	for _, sub := range subs {
		u, err := url.Parse(sub.URL)
		if err != nil {
			continue
		}
		host := u.Hostname()
		if host == "" {
			continue
		}
		host = strings.Trim(strings.ToLower(host), ".")
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		rules = append(rules, fmt.Sprintf("DOMAIN,%s,DIRECT", host))
	}
	return rules
}
