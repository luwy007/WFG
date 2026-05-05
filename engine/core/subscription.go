package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wfg/engine/models"
	"gopkg.in/yaml.v3"
)

// FetchSubscription 下载并解析订阅，返回节点列表
// direct: 是否强制直连（不走代理）
func FetchSubscription(sub *models.Subscription, direct bool) ([]models.Node, *models.UserInfo, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	if direct {
		// 强制直连：不使用任何代理
		transport.Proxy = nil
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}
	req, err := http.NewRequest("GET", sub.URL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "ClashForAndroid/2.5.12")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("下载订阅失败: %w", err)
	}
	defer resp.Body.Close()

	userInfo := parseUserInfo(resp.Header.Get("Subscription-Userinfo"))

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	format := detectFormat(body)
	sub.Format = format

	var nodes []models.Node
	switch format {
	case models.SubFormatClash:
		nodes, err = parseClashYAML(body, sub.ID)
	case models.SubFormatBase64:
		nodes, err = parseBase64URIList(body, sub.ID)
	default:
		return nil, nil, fmt.Errorf("未知订阅格式")
	}
	return nodes, userInfo, err
}

// extractNodeIPs 从节点列表中提取所有IP地址
func extractNodeIPs(nodes []models.Node) []string {
	ipSet := make(map[string]struct{})
	for _, n := range nodes {
		if server, ok := n.Raw["server"].(string); ok && server != "" {
			// 检查是否已经是IP
			if net.ParseIP(server) != nil {
				ipSet[server] = struct{}{}
			} else {
				// 尝试解析域名
				ips, err := net.LookupIP(server)
				if err == nil {
					for _, ip := range ips {
						ipSet[ip.String()] = struct{}{}
					}
				}
			}
		}
	}
	result := make([]string, 0, len(ipSet))
	for ip := range ipSet {
		result = append(result, ip)
	}
	return result
}

// detectFormat 检测订阅内容格式
func detectFormat(body []byte) models.SubFormat {
	trimmed := strings.TrimSpace(string(body))
	// Clash YAML 特征
	if strings.Contains(trimmed, "proxies:") || strings.HasPrefix(trimmed, "port:") {
		return models.SubFormatClash
	}
	// 尝试 Base64 解码
	decoded, err := base64Decode(trimmed)
	if err == nil && utf8.Valid(decoded) {
		if strings.Contains(string(decoded), "://") {
			return models.SubFormatBase64
		}
	}
	// 纯 URI 列表
	if strings.Contains(trimmed, "vmess://") || strings.Contains(trimmed, "ss://") ||
		strings.Contains(trimmed, "vless://") || strings.Contains(trimmed, "trojan://") {
		return models.SubFormatBase64
	}
	return models.SubFormatBase64
}

// ---- Clash YAML 解析 ----

type clashConfig struct {
	Proxies []map[string]any `yaml:"proxies"`
}

func parseClashYAML(data []byte, subID string) ([]models.Node, error) {
	var cfg clashConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 Clash YAML 失败: %w", err)
	}

	nodes := make([]models.Node, 0, len(cfg.Proxies))
	for i, p := range cfg.Proxies {
		name, _ := p["name"].(string)
		typ, _ := p["type"].(string)
		nodes = append(nodes, models.Node{
			ID:             fmt.Sprintf("%s-%d", subID, i),
			Name:           name,
			Type:           models.NodeType(typ),
			SubscriptionID: subID,
			Latency:        -1,
			Raw:            p,
		})
	}
	return nodes, nil
}

// ---- Base64 URI 列表解析 ----

func parseBase64URIList(data []byte, subID string) ([]models.Node, error) {
	text := strings.TrimSpace(string(data))

	// 尝试整体 Base64 解码
	if decoded, err := base64Decode(text); err == nil && utf8.Valid(decoded) {
		text = string(decoded)
	}

	lines := strings.Split(text, "\n")
	nodes := make([]models.Node, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		node, err := parseURI(line, subID, i)
		if err != nil {
			continue // 跳过无法解析的行
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func parseURI(uri, subID string, idx int) (models.Node, error) {
	switch {
	case strings.HasPrefix(uri, "vmess://"):
		return parseVMess(uri, subID, idx)
	case strings.HasPrefix(uri, "ss://"):
		return parseShadowsocks(uri, subID, idx)
	case strings.HasPrefix(uri, "vless://"):
		return parseVLESS(uri, subID, idx)
	case strings.HasPrefix(uri, "trojan://"):
		return parseTrojan(uri, subID, idx)
	default:
		return models.Node{}, fmt.Errorf("不支持的协议: %s", uri[:min(20, len(uri))])
	}
}

func parseVMess(uri, subID string, idx int) (models.Node, error) {
	b64 := strings.TrimPrefix(uri, "vmess://")
	decoded, err := base64Decode(b64)
	if err != nil {
		return models.Node{}, fmt.Errorf("VMess base64 解码失败: %w", err)
	}

	var v models.VMess
	if err := json.Unmarshal(decoded, &v); err != nil {
		return models.Node{}, fmt.Errorf("VMess JSON 解析失败: %w", err)
	}

	port, _ := strconv.Atoi(v.Port)
	raw := map[string]any{
		"name":    v.Name,
		"type":    "vmess",
		"server":  v.Address,
		"port":    port,
		"uuid":    v.UUID,
		"alterId": v.AltID,
		"cipher":  orDefault(v.Security, "auto"),
		"network": orDefault(v.Network, "tcp"),
	}
	if v.TLS == "tls" {
		raw["tls"] = true
		if v.SNI != "" {
			raw["servername"] = v.SNI
		}
	}
	if v.Network == "ws" {
		wsOpts := map[string]any{"path": orDefault(v.Path, "/")}
		if v.Host != "" {
			wsOpts["headers"] = map[string]any{"Host": v.Host}
		}
		raw["ws-opts"] = wsOpts
	}

	name := v.Name
	if name == "" {
		name = fmt.Sprintf("VMess-%d", idx)
	}

	return models.Node{
		ID:             fmt.Sprintf("%s-%d", subID, idx),
		Name:           name,
		Type:           models.NodeTypeVMess,
		SubscriptionID: subID,
		Latency:        -1,
		Raw:            raw,
	}, nil
}

func parseShadowsocks(uri, subID string, idx int) (models.Node, error) {
	// ss://BASE64(method:password)@host:port#name
	// ss://BASE64(method:password@host:port)#name   (旧格式)
	raw := strings.TrimPrefix(uri, "ss://")

	var name string
	if i := strings.Index(raw, "#"); i != -1 {
		name, _ = url.PathUnescape(raw[i+1:])
		raw = raw[:i]
	}

	var method, password, host string
	var port int

	if strings.Contains(raw, "@") {
		// 新格式: BASE64(method:password)@host:port
		parts := strings.SplitN(raw, "@", 2)
		decoded, err := base64Decode(parts[0])
		if err != nil {
			// 可能未编码
			decoded = []byte(parts[0])
		}
		mp := strings.SplitN(string(decoded), ":", 2)
		if len(mp) != 2 {
			return models.Node{}, fmt.Errorf("SS 格式错误")
		}
		method = mp[0]
		password = mp[1]

		parsed, err := url.Parse("ss://" + parts[1])
		if err != nil {
			return models.Node{}, fmt.Errorf("SS 地址解析失败: %w", err)
		}
		host = parsed.Hostname()
		port, _ = strconv.Atoi(parsed.Port())
	} else {
		// 旧格式: BASE64(method:password@host:port)
		decoded, err := base64Decode(raw)
		if err != nil {
			return models.Node{}, fmt.Errorf("SS base64 解码失败: %w", err)
		}
		parsed, err := url.Parse("ss://" + string(decoded))
		if err != nil {
			return models.Node{}, fmt.Errorf("SS 地址解析失败: %w", err)
		}
		method = parsed.User.Username()
		password, _ = parsed.User.Password()
		host = parsed.Hostname()
		port, _ = strconv.Atoi(parsed.Port())
	}

	if name == "" {
		name = fmt.Sprintf("SS-%d", idx)
	}

	return models.Node{
		ID:             fmt.Sprintf("%s-%d", subID, idx),
		Name:           name,
		Type:           models.NodeTypeShadowsocks,
		SubscriptionID: subID,
		Latency:        -1,
		Raw: map[string]any{
			"name":     name,
			"type":     "ss",
			"server":   host,
			"port":     port,
			"cipher":   method,
			"password": password,
		},
	}, nil
}

func parseVLESS(uri, subID string, idx int) (models.Node, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return models.Node{}, err
	}
	q := parsed.Query()
	name, _ := url.PathUnescape(parsed.Fragment)
	if name == "" {
		name = fmt.Sprintf("VLESS-%d", idx)
	}
	port, _ := strconv.Atoi(parsed.Port())

	raw := map[string]any{
		"name":    name,
		"type":    "vless",
		"server":  parsed.Hostname(),
		"port":    port,
		"uuid":    parsed.User.Username(),
		"network": orDefault(q.Get("type"), "tcp"),
		"flow":    q.Get("flow"),
	}
	if q.Get("security") == "tls" {
		raw["tls"] = true
		if sni := q.Get("sni"); sni != "" {
			raw["servername"] = sni
		}
	}
	if q.Get("type") == "ws" {
		raw["ws-opts"] = map[string]any{
			"path": orDefault(q.Get("path"), "/"),
			"headers": map[string]any{
				"Host": q.Get("host"),
			},
		}
	}

	return models.Node{
		ID:             fmt.Sprintf("%s-%d", subID, idx),
		Name:           name,
		Type:           models.NodeTypeVLESS,
		SubscriptionID: subID,
		Latency:        -1,
		Raw:            raw,
	}, nil
}

func parseTrojan(uri, subID string, idx int) (models.Node, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return models.Node{}, err
	}
	name, _ := url.PathUnescape(parsed.Fragment)
	if name == "" {
		name = fmt.Sprintf("Trojan-%d", idx)
	}
	port, _ := strconv.Atoi(parsed.Port())
	q := parsed.Query()

	raw := map[string]any{
		"name":     name,
		"type":     "trojan",
		"server":   parsed.Hostname(),
		"port":     port,
		"password": parsed.User.Username(),
		"sni":      q.Get("sni"),
	}

	return models.Node{
		ID:             fmt.Sprintf("%s-%d", subID, idx),
		Name:           name,
		Type:           models.NodeTypeTrojan,
		SubscriptionID: subID,
		Latency:        -1,
		Raw:            raw,
	}, nil
}

// ---- 响应头解析 ----

func parseUserInfo(header string) *models.UserInfo {
	if header == "" {
		return nil
	}
	info := &models.UserInfo{}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		v, _ := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		switch strings.TrimSpace(kv[0]) {
		case "upload":
			info.Upload = v
		case "download":
			info.Download = v
		case "total":
			info.Total = v
		case "expire":
			info.Expire = v
		}
	}
	return info
}

// ---- 工具函数 ----

func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// 补齐 padding
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	// 尝试 URL-safe 和标准两种编码
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
