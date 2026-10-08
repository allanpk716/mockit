// Package store 提供 mockit 的 SQLite 存储(提交/候选/审核)。
package store

import (
	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动注册
)

// Open 打开(必要时创建)数据目录下的 SQLite 库。
func Open(dataDir string) (*Store, error) {
	return nil, errNotImplemented
}

var errNotImplemented = &NotImplemented{}

// NotImplemented 表示该桩尚未实施(票 01 落地)。
type NotImplemented struct{}

func (e *NotImplemented) Error() string { return "store: 未实施(票 01)" }

// Store 是存储句柄。
type Store struct{}
