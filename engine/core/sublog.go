package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wfg/engine/models"
)

const maxLogEntries = 500 // 最多保留500条日志

var (
	subLogMu sync.RWMutex
)

// SubLogPath 订阅日志文件路径
func SubLogPath() string {
	return filepath.Join(DataDir(), "subscription_logs.json")
}

// AppendSubLog 追加一条订阅日志
func AppendSubLog(entry models.SubLogEntry) error {
	subLogMu.Lock()
	defer subLogMu.Unlock()

	entries, err := loadSubLogLocked()
	if err != nil {
		return err
	}

	entries = append([]models.SubLogEntry{entry}, entries...)
	if len(entries) > maxLogEntries {
		entries = entries[:maxLogEntries]
	}

	return saveSubLogLocked(entries)
}

// GetSubLogs 获取订阅日志，支持按 subID 过滤
func GetSubLogs(subID string) ([]models.SubLogEntry, error) {
	subLogMu.RLock()
	defer subLogMu.RUnlock()

	entries, err := loadSubLogLocked()
	if err != nil {
		return nil, err
	}

	if subID == "" {
		return entries, nil
	}

	var filtered []models.SubLogEntry
	for _, e := range entries {
		if e.SubID == subID {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// ClearSubLogs 清空订阅日志
func ClearSubLogs() error {
	subLogMu.Lock()
	defer subLogMu.Unlock()
	return saveSubLogLocked(nil)
}

func loadSubLogLocked() ([]models.SubLogEntry, error) {
	path := SubLogPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []models.SubLogEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取订阅日志失败: %w", err)
	}
	var entries []models.SubLogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return []models.SubLogEntry{}, nil // 损坏时重置
	}
	return entries, nil
}

func saveSubLogLocked(entries []models.SubLogEntry) error {
	path := SubLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
