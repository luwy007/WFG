package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wfg/engine/api"
	"github.com/wfg/engine/core"
)

func main() {
	port      := flag.Int("port", 19090, "WFG 引擎 API 端口")
	parentPID := flag.Int("parent-pid", 0, "父进程 PID，消失后自动退出")
	flag.Parse()

	// 若指定了父进程 PID，启动守护 goroutine：父进程消失则自动退出
	if *parentPID > 0 {
		go func() {
			for {
				time.Sleep(2 * time.Second)
				proc, err := os.FindProcess(*parentPID)
				if err != nil {
					break
				}
				// Signal(0) 不发信号，只探测进程是否存在
				if err := proc.Signal(syscall.Signal(0)); err != nil {
					break
				}
			}
			fmt.Println("[WFG] 父进程已退出，引擎自动停止")
			core.GetManager().StopAutoRefreshScheduler()
			_ = core.DisableSystemProxy()
			_ = core.GetManager().Stop()
			os.Exit(0)
		}()
	}

	// 初始化 Manager
	mgr := core.GetManager()
	if err := mgr.Init(); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	fmt.Printf("WFG 引擎启动 (数据目录: %s)\n", core.DataDir())

	// 监听系统信号，优雅退出
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Println("\n收到退出信号，清理中...")
		core.GetManager().StopAutoRefreshScheduler()
		_ = core.DisableSystemProxy()
		_ = mgr.Stop()
		os.Exit(0)
	}()

	// 启动 REST API（阻塞）
	if err := api.Start(*port); err != nil {
		log.Fatalf("API 服务器启动失败: %v", err)
	}
}
