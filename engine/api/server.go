package api

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wfg/engine/core"
	"github.com/wfg/engine/models"
)

// Start 启动 REST API 服务器
func Start(port int) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(corsMiddleware())

	registerRoutes(r)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("WFG Engine API 启动: http://%s\n", addr)
	return r.Run(addr)
}

func registerRoutes(r *gin.Engine) {
	mgr := core.GetManager()

	// ---- 引擎状态 ----
	r.GET("/api/status", func(c *gin.Context) {
		proxyEnabled, proxyPort := core.GetSystemProxyStatus()
		cfg := mgr.GetConfig()
		switching, switchErr := mgr.SwitchingStatus()
		c.JSON(http.StatusOK, gin.H{
			"running":        mgr.IsRunning(),
			"proxy_enabled":  proxyEnabled,
			"proxy_port":     proxyPort,
			"selected_node":  mgr.GetSelectedNode(),
			"proxy_mode":     string(cfg.ProxyMode),
			"tun_enabled":    cfg.TunEnabled,
			"tun_switching":  switching,
			"tun_switch_err": switchErr,
			"version":        "0.1.0",
		})
	})

	r.POST("/api/tun", func(c *gin.Context) {
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		cfg := mgr.GetConfig()
		if cfg.TunEnabled == req.Enabled {
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}
		// 切换中不允许重复操作
		if switching, _ := mgr.SwitchingStatus(); switching {
			c.JSON(http.StatusConflict, gin.H{"error": "TUN 切换进行中，请稍候"})
			return
		}

		// 立即响应，后台执行重启（TUN 切换需要 stop+start，耗时较长）
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "switching": true})

		go func() {
			mgr.SetSwitching(true, "")
			defer func() { mgr.SetSwitching(false, "") }()

			cfg.TunEnabled = req.Enabled
			wasRunning := mgr.IsRunning()
			if wasRunning {
				_ = core.DisableSystemProxy()
				_ = mgr.Stop()
			}
			if err := mgr.UpdateConfig(cfg); err != nil {
				mgr.SetSwitching(false, err.Error())
				return
			}
			if wasRunning {
				if err := mgr.Start(); err != nil {
					mgr.SetSwitching(false, err.Error())
					return
				}
				if !req.Enabled {
					_ = core.EnableSystemProxy(cfg.MixedPort)
				}
			}
		}()
	})

	r.POST("/api/proxy/mode", func(c *gin.Context) {
		var req struct {
			Mode string `json:"mode" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		mode := models.ProxyMode(req.Mode)
		if mode != models.ProxyModeRule && mode != models.ProxyModeGlobal && mode != models.ProxyModeDirect {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mode 必须为 rule / global / direct"})
			return
		}
		cfg := mgr.GetConfig()
		cfg.ProxyMode = mode
		if err := mgr.UpdateConfig(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "mode": req.Mode})
	})

	// ---- 代理控制 ----
	r.POST("/api/proxy/start", func(c *gin.Context) {
		if err := mgr.Start(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		cfg := mgr.GetConfig()
		// TUN 模式下流量已被虚拟网卡接管，不要再设系统代理（避免死循环）
		if !cfg.TunEnabled {
			if err := core.EnableSystemProxy(cfg.MixedPort); err != nil {
				c.JSON(http.StatusOK, gin.H{"ok": true, "warn": err.Error()})
				return
			}
		} else {
			_ = core.DisableSystemProxy()
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/proxy/stop", func(c *gin.Context) {
		if err := core.DisableSystemProxy(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := mgr.Stop(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- 订阅管理 ----
	r.GET("/api/subscriptions", func(c *gin.Context) {
		cfg := mgr.GetConfig()
		// 返回时隐藏节点详情，减少传输量
		subs := make([]gin.H, 0, len(cfg.Subscriptions))
		for _, s := range cfg.Subscriptions {
			subs = append(subs, gin.H{
				"id":               s.ID,
				"name":             s.Name,
				"url":              s.URL,
				"format":           s.Format,
				"updated_at":       s.UpdatedAt,
				"node_count":       s.NodeCount,
				"user_info":        s.UserInfo,
				"auto_refresh":     s.AutoRefresh,
				"refresh_interval": s.RefreshInterval,
			})
		}
		c.JSON(http.StatusOK, subs)
	})

	r.POST("/api/subscriptions", func(c *gin.Context) {
		var req struct {
			Name            string `json:"name" binding:"required"`
			URL             string `json:"url"  binding:"required"`
			AutoRefresh     bool   `json:"auto_refresh"`
			RefreshInterval int    `json:"refresh_interval"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		interval := req.RefreshInterval
		if interval < 1 || interval > 300 {
			interval = 60
		}

		cfg := mgr.GetConfig()
		sub := models.Subscription{
			ID:              fmt.Sprintf("sub_%d", time.Now().UnixMilli()),
			Name:            req.Name,
			URL:             req.URL,
			AutoRefresh:     req.AutoRefresh,
			RefreshInterval: interval,
		}
		cfg.Subscriptions = append(cfg.Subscriptions, sub)
		if err := mgr.UpdateConfig(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 立即刷新
		if err := mgr.RefreshSubscription(sub.ID); err != nil {
			c.JSON(http.StatusOK, gin.H{"id": sub.ID, "warn": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": sub.ID, "ok": true})
	})

	r.PUT("/api/subscriptions/:id", func(c *gin.Context) {
		id := c.Param("id")
		var req struct {
			AutoRefresh     bool `json:"auto_refresh"`
			RefreshInterval int  `json:"refresh_interval"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		interval := req.RefreshInterval
		if interval < 1 || interval > 300 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_interval 必须在 1 到 300 分钟之间"})
			return
		}

		cfg := mgr.GetConfig()
		found := false
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].ID == id {
				cfg.Subscriptions[i].AutoRefresh = req.AutoRefresh
				cfg.Subscriptions[i].RefreshInterval = interval
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在"})
			return
		}
		if err := mgr.UpdateConfig(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.DELETE("/api/subscriptions/:id", func(c *gin.Context) {
		id := c.Param("id")
		cfg := mgr.GetConfig()
		newSubs := cfg.Subscriptions[:0]
		for _, s := range cfg.Subscriptions {
			if s.ID != id {
				newSubs = append(newSubs, s)
			}
		}
		cfg.Subscriptions = newSubs
		if err := mgr.UpdateConfig(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/subscriptions/:id/refresh", func(c *gin.Context) {
		id := c.Param("id")
		if err := mgr.RefreshSubscription(id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- 节点管理 ----
	r.GET("/api/nodes", func(c *gin.Context) {
		nodes := mgr.GetNodes()
		c.JSON(http.StatusOK, nodes)
	})

	// ?name=节点名  (query 参数避免节点名含特殊字符时 path 解析错误)
	r.GET("/api/nodes/latency", func(c *gin.Context) {
		name := c.Query("name")
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 name 参数"})
			return
		}
		latency, err := mgr.TestNodeLatency(name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"latency_ms": latency})
	})

	r.POST("/api/nodes/select", func(c *gin.Context) {
		var req struct {
			Group string `json:"group"`
			Node  string `json:"node" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		group := req.Group
		if group == "" {
			group = "手动选择"
		}
		if err := mgr.SelectNode(group, req.Node); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- 配置管理 ----
	r.GET("/api/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, mgr.GetConfig())
	})

	r.PUT("/api/config", func(c *gin.Context) {
		var cfg models.AppConfig
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := mgr.UpdateConfig(&cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- 订阅日志 ----
	r.GET("/api/subscriptions/logs", func(c *gin.Context) {
		subID := c.Query("sub_id")
		entries, err := core.GetSubLogs(subID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, entries)
	})

	r.DELETE("/api/subscriptions/logs", func(c *gin.Context) {
		if err := core.ClearSubLogs(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- 分流规则 ----
	r.GET("/api/rules", func(c *gin.Context) {
		cfg := mgr.GetConfig()
		c.JSON(http.StatusOK, cfg.Rules)
	})

	r.PUT("/api/rules", func(c *gin.Context) {
		var rules []models.Rule
		if err := c.ShouldBindJSON(&rules); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		cfg := mgr.GetConfig()
		cfg.Rules = rules
		if err := mgr.UpdateConfig(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- Geo 数据库状态 ----
	r.GET("/api/geo/status", func(c *gin.Context) {
		files := []string{"geoip.dat", "geosite.dat", "country.mmdb"}
		ready := true
		details := make(map[string]bool)
		for _, f := range files {
			path := fmt.Sprintf("%s/geo/%s", core.DataDir(), f)
			info, err := os.Stat(path)
			ok := err == nil && info.Size() > 1024*100 // 至少 100KB
			details[f] = ok
			if !ok {
				ready = false
			}
		}
		c.JSON(http.StatusOK, gin.H{"ready": ready, "files": details})
	})

	// ---- Geo 数据库下载 ----
	r.POST("/api/geo/update", func(c *gin.Context) {
		cfg := mgr.GetConfig()
		geoDir := filepath.Join(core.DataDir(), "geo")
		if err := os.MkdirAll(geoDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 优先通过本地代理下载（端口 7890），若未运行则直连
		var transport *http.Transport
		if mgr.IsRunning() {
			proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", cfg.MixedPort))
			transport = &http.Transport{
				Proxy:           http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		} else {
			transport = &http.Transport{}
		}
		client := &http.Client{Transport: transport, Timeout: 120 * time.Second}

		type geoFile struct {
			name string
			src  string
		}
		files := []geoFile{
			{"geoip.dat", "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat"},
			{"geosite.dat", "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"},
			{"country.mmdb", "https://ghproxy.net/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb"},
		}

		var failed []string
		for _, f := range files {
			dest := filepath.Join(geoDir, f.name)
			resp, err := client.Get(f.src)
			if err != nil {
				failed = append(failed, f.name+": "+err.Error())
				continue
			}
			tmp := dest + ".tmp"
			out, err := os.Create(tmp)
			if err != nil {
				resp.Body.Close()
				failed = append(failed, f.name+": "+err.Error())
				continue
			}
			_, err = io.Copy(out, resp.Body)
			out.Close()
			resp.Body.Close()
			if err != nil {
				os.Remove(tmp)
				failed = append(failed, f.name+": "+err.Error())
				continue
			}
			os.Rename(tmp, dest)
		}

		if len(failed) > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("部分文件下载失败: %v", failed)})
			return
		}

		// 触发 mihomo 重载配置（让新 geo 数据生效）
		if mgr.IsRunning() {
			_ = mgr.MihomoAPIPut("/configs?force=true", map[string]any{"path": core.MihomoConfigPath()})
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ---- 境外连通性检测 ----
	r.GET("/api/ping", func(c *gin.Context) {
		cfg := mgr.GetConfig()
		if !mgr.IsRunning() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "代理未运行"})
			return
		}

		proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", cfg.MixedPort))
		transport := &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client := &http.Client{
			Transport: transport,
			Timeout:   8 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		type site struct{ name, addr string }
		sites := []site{
			{"google", "https://www.google.com"},
			{"x", "https://x.com"},
			{"youtube", "https://www.youtube.com"},
			{"baidu", "https://www.baidu.com"},
			{"xiaohongshu", "https://www.xiaohongshu.com"},
			{"tencent", "https://www.tencent.com"},
		}

		results := make(map[string]int)
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, s := range sites {
			wg.Add(1)
			go func(name, addr string) {
				defer wg.Done()
				start := time.Now()
				resp, err := client.Get(addr)
				ms := 0
				if err == nil {
					resp.Body.Close()
					ms = int(time.Since(start).Milliseconds())
				}
				mu.Lock()
				results[name] = ms
				mu.Unlock()
			}(s.name, s.addr)
		}
		wg.Wait()
		c.JSON(http.StatusOK, results)
	})

	// ---- mihomo 原生 API 透传（供 Mac App 直接调用流量统计等） ----
	r.GET("/api/mihomo/*path", func(c *gin.Context) {
		path := c.Param("path")
		result, err := mgr.MihomoAPIGet(path)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
