//go:build !windows

// 该文件负责非 Windows 环境下的 Named Pipe 占位实现。
package rpc

import (
	"context"
	"errors"
	"net"
)

// errNamedPipeUnsupported 定义当前模块的基础变量。
var errNamedPipeUnsupported = errors.New("named pipe transport unsupported")

// serveNamedPipe 处理当前模块的相关逻辑。
func serveNamedPipe(ctx context.Context, pipeName string, handler func(net.Conn)) error {
	_ = ctx
	_ = pipeName
	_ = handler
	return errNamedPipeUnsupported
}
