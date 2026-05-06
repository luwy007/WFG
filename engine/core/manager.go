package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wfg/engine/models"
)

// Manager 管理 mihomo 进程和应用状态
type Manager struct {
	mu              sync.RWMutex
	cfg             *models.AppConfig
	nodes           []models.Node // 所有节点（来自各订阅）
	process         *exec.Cmd
	running         bool
	stopping        bool          // 主动停止中，避免把用户停止误判为异常退出
	switching       bool          // TUN 模式切换中
	switchErr       string        // 最近一次切换的错误信息
	autoRefreshStop chan struct{} // 自动刷新停止信号
}

var globalManager *Manager

// GetManager 获取全局 Manager 单例
func GetManager() *Manager {
	if globalManager == nil {
		globalManager = &Manager{}
	}
	return globalManager
}

// Init 初始化：加载配置、确保数据目录存在，启动自动刷新调度器
func (m *Manager) Init() error {
	if err := os.MkdirAll(DataDir(), 0755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	if err := os.MkdirAll(GeoDataDir(), 0755); err != nil {
		return fmt.Errorf("创建 geo 目录失败: %w", err)
	}

	cfg, err := LoadAppConfig()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	m.cfg = cfg
	m.rebuildNodes() // 用磁盘上已有的订阅节点填充内存，避免 GetNodes() 返回 null
	m.startAutoRefreshScheduler()
	return nil
}

// startAutoRefreshScheduler 启动自动刷新后台调度器
func (m *Manager) startAutoRefreshScheduler() {
	if m.autoRefreshStop != nil {
		close(m.autoRefreshStop)
	}
	m.autoRefreshStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.checkAndAutoRefresh()
			case <-m.autoRefreshStop:
				return
			}
		}
	}()
}

// StopAutoRefreshScheduler 停止自动刷新调度器（供优雅退出使用）
func (m *Manager) StopAutoRefreshScheduler() {
	if m.autoRefreshStop != nil {
		close(m.autoRefreshStop)
		m.autoRefreshStop = nil
	}
}

// checkAndAutoRefresh 检查并执行自动刷新
func (m *Manager) checkAndAutoRefresh() {
	m.mu.RLock()
	type refreshCandidate struct {
		id       string
		name     string
		interval int
	}
	now := time.Now()
	var candidates []refreshCandidate
	for i := range m.cfg.Subscriptions {
		sub := m.cfg.Subscriptions[i]
		if !sub.AutoRefresh {
			continue
		}
		interval := sub.RefreshInterval
		if interval < 1 || interval > 300 {
			interval = 60 // 默认60分钟
		}
		lastRefreshBase := sub.UpdatedAt
		if sub.AutoRefreshFrom.After(lastRefreshBase) {
			lastRefreshBase = sub.AutoRefreshFrom
		}
		// 如果从未更新过，或者已经超过间隔时间
		if lastRefreshBase.IsZero() || now.Sub(lastRefreshBase) >= time.Duration(interval)*time.Minute {
			candidates = append(candidates, refreshCandidate{
				id:       sub.ID,
				name:     sub.Name,
				interval: interval,
			})
		}
	}
	m.mu.RUnlock()

	for _, sub := range candidates {
		fmt.Printf("[auto-refresh] 订阅 %s 自动刷新触发 (间隔 %d 分钟)\n", sub.name, sub.interval)
		if err := AppendSubLog(models.SubLogEntry{
			Time:    time.Now(),
			SubID:   sub.id,
			SubName: sub.name,
			Success: true,
			Message: fmt.Sprintf("自动刷新触发：间隔 %d 分钟，开始检查订阅节点", sub.interval),
		}); err != nil {
			fmt.Printf("[auto-refresh] 订阅 %s 触发日志写入失败: %v\n", sub.name, err)
		}
		if err := m.RefreshSubscriptionWithSource(sub.id, "自动刷新"); err != nil {
			fmt.Printf("[auto-refresh] 订阅 %s 自动刷新失败: %v\n", sub.name, err)
		} else {
			fmt.Printf("[auto-refresh] 订阅 %s 自动刷新成功\n", sub.name)
		}
	}
}

// GetConfig 获取当前配置（只读）
func (m *Manager) GetConfig() *models.AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// UpdateConfig 更新配置并重新生成 mihomo 配置
func (m *Manager) UpdateConfig(newCfg *models.AppConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = newCfg
	if err := SaveAppConfig(newCfg); err != nil {
		return err
	}
	// 如果正在运行则热重载
	if m.running {
		return m.reloadMihomo()
	}
	return nil
}

// GetNodes 获取所有节点
func (m *Manager) GetNodes() []models.Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes
}

// RefreshSubscription 刷新指定订阅
func (m *Manager) RefreshSubscription(subID string) error {
	return m.RefreshSubscriptionWithSource(subID, "手动刷新")
}

// RefreshSubscriptionWithSource 刷新指定订阅，并把触发来源写入日志
func (m *Manager) RefreshSubscriptionWithSource(subID, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if source == "" {
		source = "刷新"
	}

	var sub *models.Subscription
	for i := range m.cfg.Subscriptions {
		if m.cfg.Subscriptions[i].ID == subID {
			sub = &m.cfg.Subscriptions[i]
			break
		}
	}
	if sub == nil {
		return fmt.Errorf("订阅 %s 不存在", subID)
	}

	// 订阅请求始终强制直连，避免走代理网络导致失败
	nodes, userInfo, err := FetchSubscription(sub, true)

	// 记录日志（无论成功失败）
	logEntry := models.SubLogEntry{
		Time:    time.Now(),
		SubID:   sub.ID,
		SubName: sub.Name,
	}
	if err != nil {
		logEntry.Success = false
		logEntry.Message = fmt.Sprintf("%s失败：%s", source, err.Error())
		_ = AppendSubLog(logEntry)
		return fmt.Errorf("刷新订阅失败: %w", err)
	}

	sub.Nodes = nodes
	sub.NodeCount = len(nodes)
	sub.UpdatedAt = time.Now()
	if userInfo != nil {
		sub.UserInfo = userInfo
	}

	logEntry.Success = true
	logEntry.NodeCount = len(nodes)
	logEntry.IPs = extractNodeIPs(nodes)
	if len(logEntry.IPs) > 0 {
		logEntry.Message = fmt.Sprintf("%s成功：解析 %d 个节点，提取 %d 个 IP", source, len(nodes), len(logEntry.IPs))
	} else {
		logEntry.Message = fmt.Sprintf("%s成功：解析 %d 个节点", source, len(nodes))
	}
	_ = AppendSubLog(logEntry)

	// 更新全量节点列表
	m.rebuildNodes()

	if err := SaveAppConfig(m.cfg); err != nil {
		return err
	}
	if m.running {
		return m.reloadMihomo()
	}
	return nil
}

// rebuildNodes 从所有订阅重建节点列表（调用时需持有锁）
func (m *Manager) rebuildNodes() {
	nodes := make([]models.Node, 0)
	for _, sub := range m.cfg.Subscriptions {
		nodes = append(nodes, sub.Nodes...)
	}
	m.nodes = nodes
}

// Start 启动 mihomo 进程
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("已在运行中")
	}

	// 确保 mihomo 二进制存在
	if err := m.ensureMihomoBinary(); err != nil {
		return fmt.Errorf("mihomo 准备失败: %w", err)
	}

	// 预下载 GeoData，减少 mihomo 启动时阻塞；失败不致命，由 mihomo 自行处理
	if err := m.ensureGeoData(); err != nil {
		fmt.Printf("GeoData 预下载失败（将由 mihomo 自行处理）: %v\n", err)
	}

	// 重建节点并生成配置
	m.rebuildNodes()
	if err := GenerateMihomoConfig(m.cfg, m.nodes); err != nil {
		return fmt.Errorf("生成配置失败: %w", err)
	}

	// TUN 模式需要 root 创建 utun。root 引擎可直接 fork；普通用户引擎保留 osascript 兜底。
	// 注意：m.running 在 waitForMihomoAPI 确认就绪后才设为 true，
	// 避免在 API 未就绪窗口内 reloadMihomo 被误触发。
	elevatedTun := m.cfg.TunEnabled && os.Geteuid() != 0
	if elevatedTun {
		if err := m.startMihomoElevated(); err != nil {
			return fmt.Errorf("以管理员权限启动 mihomo 失败: %w", err)
		}
	} else {
		if err := m.startMihomoDirect(); err != nil {
			return err
		}
	}

	// GeoData 已预下载时 mihomo 启动更快，适当缩短超时
	apiTimeout := 90 * time.Second
	if isGeoDataReady() {
		apiTimeout = 30 * time.Second
	}
	if err := m.waitForMihomoAPI(apiTimeout); err != nil {
		_ = m.stopLocked()
		return fmt.Errorf("mihomo 启动超时: %w\nmihomo 最近日志:\n%s", err, tailMihomoLog(40))
	}

	// API 就绪后才标记为运行中
	m.running = true

	// 普通用户 TUN：提权进程脱离了 m.process 管理，启动后台 goroutine 轮询 PID 存活状态
	if elevatedTun {
		go m.watchTunProcess()
	}

	// 恢复上次选中的节点
	if m.cfg.SelectedNode != "" {
		_ = m.MihomoAPIPut("/proxies/"+url.PathEscape("手动选择"), map[string]any{
			"name": m.cfg.SelectedNode,
		})
	}

	return nil
}

func (m *Manager) startMihomoDirect() error {
	logFile, err := os.OpenFile(filepath.Join(DataDir(), "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开 mihomo 日志失败: %w", err)
	}

	cmd := exec.Command(MihomoBinPath(), "-f", MihomoConfigPath(), "-d", DataDir())
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动 mihomo 失败: %w", err)
	}
	m.process = cmd
	fmt.Printf("[mihomo] 直接启动 pid=%d root=%t tun=%t\n", cmd.Process.Pid, os.Geteuid() == 0, m.cfg.TunEnabled)
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		m.mu.Lock()
		wasRunning := m.running
		expectedStop := m.stopping
		shouldRestart := wasRunning && !expectedStop && os.Geteuid() == 0 && m.cfg != nil && m.cfg.TunEnabled
		m.running = false
		m.process = nil
		m.mu.Unlock()
		if wasRunning {
			fmt.Printf("[mihomo] 进程退出 pid=%d err=%v，最后日志：\n%s\n", cmd.Process.Pid, err, tailMihomoLog(40))
		}
		if shouldRestart {
			go m.restartMihomoWithoutPrompt()
		}
	}()
	return nil
}

// Stop 停止 mihomo 进程
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

func (m *Manager) stopLocked() error {
	// 检查是否有需要清理的进程：running=true，或进程已启动但 API 尚未确认（m.process != nil）
	tunPIDExists := func() bool {
		data, _ := os.ReadFile(mihomoPIDFile())
		return len(strings.TrimSpace(string(data))) > 0
	}
	if !m.running && m.process == nil && !tunPIDExists() {
		return nil
	}
	// TUN 模式下是提权启动的游离进程，没有 m.process
	if m.process == nil {
		fmt.Println("[mihomo] 停止 TUN 模式 mihomo")
		_ = stopMihomoElevated()
		m.running = false
	} else {
		fmt.Printf("[mihomo] 停止普通模式 mihomo pid=%d\n", m.process.Process.Pid)
		m.stopping = true
		if err := m.process.Process.Kill(); err != nil {
			m.stopping = false
			return fmt.Errorf("停止 mihomo 失败: %w", err)
		}
		_ = m.process.Wait()
		m.running = false
		m.process = nil
		m.stopping = false
	}
	// 并行等待两个端口释放，避免立即重启时 bind 冲突
	var wg sync.WaitGroup
	for _, port := range []int{m.cfg.APIPort, m.cfg.MixedPort} {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			waitForPortFree(p, 3*time.Second)
		}(port)
	}
	wg.Wait()
	return nil
}

// waitForPortFree 轮询直到 TCP 端口不再被监听（OS 回收需要短暂时间）
func waitForPortFree(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return // 端口已释放
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

// mihomoPIDFile TUN 模式下 PID 文件路径
func mihomoPIDFile() string {
	return filepath.Join(DataDir(), "mihomo.pid")
}

// startMihomoElevated 通过 osascript 弹出管理员授权框，以 root 启动 mihomo
func (m *Manager) startMihomoElevated() error {
	_ = os.Remove(mihomoPIDFile())
	logPath := filepath.Join(DataDir(), "mihomo.log")
	// 不用 nohup：osascript 的 do shell script 本身没有控制终端，
	// nohup 会因无 tty 报 "can't detach from console" 并中止命令。
	// 直接用 & 后台运行即可；echo $! 写 PID 供后续 kill。
	shellCmd := fmt.Sprintf(
		`'%s' -f '%s' -d '%s' >'%s' 2>&1 & echo $! > '%s'`,
		MihomoBinPath(), MihomoConfigPath(), DataDir(), logPath, mihomoPIDFile(),
	)
	script := fmt.Sprintf(
		`do shell script "%s" with administrator privileges`,
		strings.ReplaceAll(shellCmd, `"`, `\"`),
	)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript 执行失败: %w, 输出: %s", err, string(out))
	}
	return nil
}

// watchTunProcess 轮询 TUN 模式下提权进程的存活状态，进程退出时同步 m.running
func (m *Manager) watchTunProcess() {
	lastPID := ""
	for {
		time.Sleep(2 * time.Second)
		pidData, err := os.ReadFile(mihomoPIDFile())
		if err != nil {
			break // PID 文件消失，进程已退出
		}
		pid := strings.TrimSpace(string(pidData))
		if pid == "" {
			break
		}
		lastPID = pid
		pidNum, err := strconv.Atoi(pid)
		if err != nil {
			break
		}
		// syscall.Kill(pid, 0) 仅检测进程是否存在，不发送实际信号。
		// TUN 模式下 mihomo 以 root 运行，普通用户检查时可能返回 EPERM；
		// EPERM 表示进程存在但当前用户无权发信号，不能当作退出。
		if err := syscall.Kill(pidNum, 0); err != nil && err != syscall.EPERM {
			break // ESRCH 等错误表示进程不存在
		}
		// 若 m.running 已被外部 Stop() 置为 false，退出监控
		m.mu.RLock()
		running := m.running
		m.mu.RUnlock()
		if !running {
			return
		}
	}
	m.mu.Lock()
	wasRunning := m.running
	m.running = false
	m.mu.Unlock()
	if wasRunning {
		fmt.Printf("[mihomo] TUN 进程已退出 pid=%s，最后日志：\n%s\n", lastPID, tailMihomoLog(40))
	}
	_ = os.Remove(mihomoPIDFile())
	if wasRunning {
		m.SetSwitching(false, "TUN 进程异常退出。由于重新启动需要管理员认证，请手动点击启动。")
	}
}

func (m *Manager) restartMihomoWithoutPrompt() {
	delays := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
	for attempt, delay := range delays {
		time.Sleep(delay)

		m.mu.RLock()
		shouldRestart := m.cfg != nil && m.cfg.TunEnabled && !m.running && !m.switching
		m.mu.RUnlock()
		if !shouldRestart {
			return
		}

		fmt.Printf("[mihomo] TUN 异常退出后尝试无弹窗自动重启 (%d/%d)\n", attempt+1, len(delays))
		if err := m.Start(); err != nil {
			msg := fmt.Sprintf("TUN 异常退出后自动重启失败 (%d/%d): %v", attempt+1, len(delays), err)
			fmt.Printf("[mihomo] %s\n", msg)
			m.SetSwitching(false, msg)
			continue
		}
		m.SetSwitching(false, "")
		fmt.Println("[mihomo] TUN 异常退出后已无弹窗自动重启")
		return
	}
}

func tailMihomoLog(maxLines int) string {
	data, err := os.ReadFile(filepath.Join(DataDir(), "mihomo.log"))
	if err != nil {
		return fmt.Sprintf("读取 mihomo.log 失败: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// stopMihomoElevated 通过 osascript kill 提权启动的 mihomo
func stopMihomoElevated() error {
	pidData, err := os.ReadFile(mihomoPIDFile())
	if err != nil {
		return err
	}
	pid := strings.TrimSpace(string(pidData))
	if pid == "" {
		return nil
	}
	script := fmt.Sprintf(`do shell script "kill %s" with administrator privileges`, pid)
	_ = exec.Command("osascript", "-e", script).Run()
	_ = os.Remove(mihomoPIDFile())
	return nil
}

// IsRunning 返回是否正在运行
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// SwitchingStatus 返回 TUN 切换状态和最近一次切换错误
func (m *Manager) SwitchingStatus() (switching bool, lastErr string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.switching, m.switchErr
}

// SetSwitching 更新 TUN 切换状态（供后台 goroutine 调用）
func (m *Manager) SetSwitching(switching bool, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.switching = switching
	m.switchErr = errMsg
}

// reloadMihomo 热重载 mihomo 配置（调用时需持有写锁）
func (m *Manager) reloadMihomo() error {
	if err := GenerateMihomoConfig(m.cfg, m.nodes); err != nil {
		return err
	}
	// 调用 mihomo REST API 触发重载
	return m.MihomoAPIPut("/configs?force=true", map[string]any{
		"path": MihomoConfigPath(),
	})
}

// waitForMihomoAPI 轮询等待 mihomo REST API 就绪（指数退避：50→100→200→400→500ms）
func (m *Manager) waitForMihomoAPI(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	interval := 50 * time.Millisecond
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/version", m.cfg.APIPort))
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(interval)
		if interval < 500*time.Millisecond {
			interval *= 2
		}
	}
	return fmt.Errorf("等待超时")
}

// mihomoAPIGet 调用 mihomo REST API GET
func (m *Manager) MihomoAPIGet(path string) (map[string]any, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", m.cfg.APIPort, path)
	req, _ := http.NewRequest("GET", url, nil)
	if m.cfg.MihomoSecret != "" {
		req.Header.Set("Authorization", "Bearer "+m.cfg.MihomoSecret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (m *Manager) MihomoAPIPut(path string, body any) error {
	data, _ := json.Marshal(body)
	url := fmt.Sprintf("http://127.0.0.1:%d%s", m.cfg.APIPort, path)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if m.cfg.MihomoSecret != "" {
		req.Header.Set("Authorization", "Bearer "+m.cfg.MihomoSecret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

// TestNodeLatency 测试单个节点延迟（通过 mihomo API）
func (m *Manager) TestNodeLatency(nodeName string) (int, error) {
	if !m.running {
		return 0, fmt.Errorf("引擎未运行")
	}
	encoded := url.PathEscape(nodeName)
	testURL := url.QueryEscape("http://www.gstatic.com/generate_204")
	path := fmt.Sprintf("/proxies/%s/delay?timeout=5000&url=%s", encoded, testURL)
	result, err := m.MihomoAPIGet(path)
	if err != nil {
		return 0, err
	}
	// mihomo 超时时返回 {"message":"timeout"}，不报错只记为 0（超时）
	var latency int
	if delay, ok := result["delay"].(float64); ok {
		latency = int(delay)
	} else {
		latency = 0 // 超时或节点不可达
	}
	// 把结果写回内存中的节点列表，使 fetchNodes 能反映最新延迟
	m.mu.Lock()
	for i := range m.nodes {
		if m.nodes[i].Name == nodeName {
			m.nodes[i].Latency = latency
			break
		}
	}
	m.mu.Unlock()
	return latency, nil
}

// SelectNode 切换当前使用的节点，并持久化选择
func (m *Manager) SelectNode(groupName, nodeName string) error {
	if !m.running {
		return fmt.Errorf("引擎未运行")
	}
	if err := m.MihomoAPIPut(fmt.Sprintf("/proxies/%s", url.PathEscape(groupName)), map[string]any{
		"name": nodeName,
	}); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg.SelectedNode = nodeName
	cfg := m.cfg
	m.mu.Unlock()
	return SaveAppConfig(cfg)
}

// GetSelectedNode 返回当前选中的节点名
func (m *Manager) GetSelectedNode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.SelectedNode
}

// raceFetch 并发竞速多个镜像，返回最先成功的响应及其 cancel 函数。
// 调用方必须在读完 body 后依次调用 cancel() 和 resp.Body.Close()。
func raceFetch(mirrors []string, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	type result struct {
		resp   *http.Response
		cancel context.CancelFunc
	}
	ch := make(chan result, len(mirrors))
	deadline := time.Now().Add(timeout)

	for _, u := range mirrors {
		go func(u string) {
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				cancel()
				ch <- result{}
				return
			}
			r, err := http.DefaultClient.Do(req)
			if err != nil || r.StatusCode != 200 {
				cancel()
				if r != nil {
					r.Body.Close()
				}
				ch <- result{}
				return
			}
			ch <- result{resp: r, cancel: cancel}
		}(u)
	}

	var winner result
	for range mirrors {
		res := <-ch
		if res.resp != nil {
			if winner.resp == nil {
				winner = res
			} else {
				res.cancel()
				res.resp.Body.Close()
			}
		}
	}
	if winner.resp == nil {
		return nil, nil, fmt.Errorf("所有镜像均失败")
	}
	return winner.resp, winner.cancel, nil
}

// ensureMihomoBinary 确保 mihomo 二进制文件存在，不存在则并发竞速镜像下载
func (m *Manager) ensureMihomoBinary() error {
	binPath := MihomoBinPath()
	if _, err := os.Stat(binPath); err == nil {
		return nil // 已存在
	}

	// 用 uname -m 获取真实硬件架构（runtime.GOARCH 在 Rosetta 下会返回 amd64）
	unameOut, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return fmt.Errorf("获取架构失败: %w", err)
	}
	hwArch := strings.TrimSpace(string(unameOut))

	const version = "v1.18.7"
	var filename string
	switch hwArch {
	case "arm64":
		filename = fmt.Sprintf("mihomo-darwin-arm64-%s.gz", version)
	default:
		// Intel Mac：使用 compatible 版本，兼容不支持 v3 指令集的旧 CPU
		filename = fmt.Sprintf("mihomo-darwin-amd64-compatible-%s.gz", version)
	}

	ghPath := fmt.Sprintf("MetaCubeX/mihomo/releases/download/%s/%s", version, filename)
	mirrors := []string{
		"https://ghproxy.net/https://github.com/" + ghPath,
		"https://mirror.ghproxy.com/https://github.com/" + ghPath,
		"https://github.com/" + ghPath,
	}

	fmt.Printf("正在下载 mihomo (%s)，并发竞速 %d 个镜像...\n", hwArch, len(mirrors))
	resp, cancel, err := raceFetch(mirrors, 60*time.Second)
	if err != nil {
		return fmt.Errorf("所有下载源均失败，请手动下载 mihomo 放到 %s", binPath)
	}
	defer cancel()
	defer resp.Body.Close()

	tmpGz := binPath + ".gz"
	f, err := os.Create(tmpGz)
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpGz)
		return err
	}
	f.Close()

	out, err := exec.Command("sh", "-c", fmt.Sprintf("gunzip -c %q > %q", tmpGz, binPath)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("解压失败: %s", out)
	}
	os.Remove(tmpGz)
	return os.Chmod(binPath, 0755)
}

// isGeoDataReady 检查三个 GeoData 文件是否均已存在且有效（>100KB）
func isGeoDataReady() bool {
	for _, name := range []string{"geoip.dat", "geosite.dat", "country.mmdb"} {
		info, err := os.Stat(filepath.Join(GeoDataDir(), name))
		if err != nil || info.Size() <= 100*1024 {
			return false
		}
	}
	return true
}

// ensureGeoData 检查 GeoData 文件是否完整，缺失则并发下载
func (m *Manager) ensureGeoData() error {
	type geoFile struct {
		name string
		url  string
	}
	all := []geoFile{
		{"geoip.dat", "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat"},
		{"geosite.dat", "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"},
		{"country.mmdb", "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb"},
	}

	geoDir := GeoDataDir()
	var missing []geoFile
	for _, f := range all {
		info, err := os.Stat(filepath.Join(geoDir, f.name))
		if err != nil || info.Size() <= 100*1024 {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Printf("GeoData 预下载：并发获取 %d 个文件...\n", len(missing))
	client := &http.Client{Timeout: 120 * time.Second}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string

	for _, f := range missing {
		wg.Add(1)
		go func(f geoFile) {
			defer wg.Done()
			dest := filepath.Join(geoDir, f.name)
			resp, err := client.Get(f.url)
			if err != nil {
				mu.Lock()
				errs = append(errs, f.name+": "+err.Error())
				mu.Unlock()
				return
			}
			defer resp.Body.Close()

			tmp := dest + ".tmp"
			out, err := os.Create(tmp)
			if err != nil {
				mu.Lock()
				errs = append(errs, f.name+": "+err.Error())
				mu.Unlock()
				return
			}
			_, err = io.Copy(out, resp.Body)
			out.Close()
			if err != nil {
				os.Remove(tmp)
				mu.Lock()
				errs = append(errs, f.name+": "+err.Error())
				mu.Unlock()
				return
			}
			os.Rename(tmp, dest)
			fmt.Printf("GeoData 下载完成: %s\n", f.name)
		}(f)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("部分 GeoData 下载失败: %v", errs)
	}
	return nil
}
