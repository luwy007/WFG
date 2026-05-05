package core

import (
	"fmt"
	"os/exec"
	"strings"
)

// listNetworkServices 获取所有网络服务名称（Wi-Fi、Ethernet 等）
func listNetworkServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	services := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 跳过提示行和空行
		if line == "" || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

// EnableSystemProxy 将 macOS 系统代理指向本地混合端口
func EnableSystemProxy(port int) error {
	services, err := listNetworkServices()
	if err != nil {
		return fmt.Errorf("获取网络服务失败: %w", err)
	}

	host := "127.0.0.1"
	portStr := fmt.Sprintf("%d", port)
	var errs []string

	for _, svc := range services {
		// 设置并启用 HTTP 代理
		if out, err := exec.Command("networksetup", "-setwebproxy", svc, host, portStr).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s set HTTP: %s", svc, out))
		}
		if out, err := exec.Command("networksetup", "-setwebproxystate", svc, "on").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s enable HTTP: %s", svc, out))
		}
		// 设置并启用 HTTPS 代理
		if out, err := exec.Command("networksetup", "-setsecurewebproxy", svc, host, portStr).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s set HTTPS: %s", svc, out))
		}
		if out, err := exec.Command("networksetup", "-setsecurewebproxystate", svc, "on").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s enable HTTPS: %s", svc, out))
		}
		// 设置并启用 SOCKS 代理
		if out, err := exec.Command("networksetup", "-setsocksfirewallproxy", svc, host, portStr).CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s set SOCKS: %s", svc, out))
		}
		if out, err := exec.Command("networksetup", "-setsocksfirewallproxystate", svc, "on").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s enable SOCKS: %s", svc, out))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("部分代理设置失败:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

// DisableSystemProxy 关闭 macOS 系统代理
func DisableSystemProxy() error {
	services, err := listNetworkServices()
	if err != nil {
		return fmt.Errorf("获取网络服务失败: %w", err)
	}

	var errs []string
	for _, svc := range services {
		if out, err := exec.Command("networksetup", "-setwebproxystate", svc, "off").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s HTTP: %s", svc, out))
		}
		if out, err := exec.Command("networksetup", "-setsecurewebproxystate", svc, "off").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s HTTPS: %s", svc, out))
		}
		if out, err := exec.Command("networksetup", "-setsocksfirewallproxystate", svc, "off").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s SOCKS: %s", svc, out))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("部分代理关闭失败:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

// GetSystemProxyStatus 返回当前系统代理是否启用及端口
func GetSystemProxyStatus() (enabled bool, port string) {
	out, err := exec.Command("networksetup", "-getwebproxy", "Wi-Fi").Output()
	if err != nil {
		return false, ""
	}
	lines := strings.Split(string(out), "\n")
	var portVal string
	enabledStr := "No"
	for _, line := range lines {
		if strings.HasPrefix(line, "Enabled:") {
			enabledStr = strings.TrimSpace(strings.TrimPrefix(line, "Enabled:"))
		}
		if strings.HasPrefix(line, "Port:") {
			portVal = strings.TrimSpace(strings.TrimPrefix(line, "Port:"))
		}
	}
	return strings.EqualFold(enabledStr, "Yes"), portVal
}
