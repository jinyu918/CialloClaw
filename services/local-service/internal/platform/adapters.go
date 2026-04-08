// 该文件负责跨平台抽象接口或平台适配实现。
package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// FileSystemAdapter 定义当前模块的接口约束。
type FileSystemAdapter interface {
	Join(parts ...string) string
	Clean(path string) string
	Abs(path string) (string, error)
	Rel(base, target string) (string, error)
	Normalize(path string) string
	EnsureWithinWorkspace(path string) (string, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, content []byte) error
	Move(src, dst string) error
	MkdirAll(path string) error
}

// PathPolicy 定义当前模块的接口约束。
type PathPolicy interface {
	Normalize(path string) string
	EnsureWithinWorkspace(path string) (string, error)
}

// OSCapabilityAdapter 定义当前模块的接口约束。
type OSCapabilityAdapter interface {
	Notify(title, body string) error
	OpenExternal(target string) error
	EnsureNamedPipe(pipeName string) error
	CloseNamedPipe(pipeName string) error
}

// ExecutionBackendAdapter 定义当前模块的接口约束。
type ExecutionBackendAdapter interface {
	Name() string
}

// StorageAdapter 定义当前模块的接口约束。
type StorageAdapter interface {
	DatabasePath() string
}

// LocalPathPolicy 定义当前模块的数据结构。
type LocalPathPolicy struct {
	workspaceRoot string
}

// NewLocalPathPolicy 创建并返回LocalPathPolicy。
func NewLocalPathPolicy(workspaceRoot string) (*LocalPathPolicy, error) {
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}

	return &LocalPathPolicy{workspaceRoot: filepath.Clean(absRoot)}, nil
}

// Normalize 处理当前模块的相关逻辑。
func (p *LocalPathPolicy) Normalize(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// EnsureWithinWorkspace 处理当前模块的相关逻辑。
func (p *LocalPathPolicy) EnsureWithinWorkspace(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	cleanTarget := filepath.Clean(absPath)
	rootWithSeparator := p.workspaceRoot + string(os.PathSeparator)

	if cleanTarget == p.workspaceRoot || strings.HasPrefix(cleanTarget, rootWithSeparator) {
		return cleanTarget, nil
	}

	return "", errors.New("path outside workspace")
}

// LocalFileSystemAdapter 定义当前模块的数据结构。
type LocalFileSystemAdapter struct {
	policy *LocalPathPolicy
}

// NewLocalFileSystemAdapter 创建并返回LocalFileSystemAdapter。
func NewLocalFileSystemAdapter(policy *LocalPathPolicy) *LocalFileSystemAdapter {
	return &LocalFileSystemAdapter{policy: policy}
}

// Join 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) Join(parts ...string) string {
	return filepath.Join(parts...)
}

// Clean 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) Clean(path string) string {
	return filepath.Clean(path)
}

// Abs 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) Abs(path string) (string, error) {
	return filepath.Abs(path)
}

// Rel 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) Rel(base, target string) (string, error) {
	return filepath.Rel(base, target)
}

// Normalize 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) Normalize(path string) string {
	return a.policy.Normalize(path)
}

// EnsureWithinWorkspace 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) EnsureWithinWorkspace(path string) (string, error) {
	return a.policy.EnsureWithinWorkspace(path)
}

// ReadFile 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) ReadFile(path string) ([]byte, error) {
	safePath, err := a.policy.EnsureWithinWorkspace(path)
	if err != nil {
		return nil, err
	}

	return os.ReadFile(safePath)
}

// WriteFile 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) WriteFile(path string, content []byte) error {
	safePath, err := a.policy.EnsureWithinWorkspace(path)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(safePath), 0o755); err != nil {
		return err
	}

	return os.WriteFile(safePath, content, 0o644)
}

// Move 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) Move(src, dst string) error {
	safeSrc, err := a.policy.EnsureWithinWorkspace(src)
	if err != nil {
		return err
	}

	safeDst, err := a.policy.EnsureWithinWorkspace(dst)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(safeDst), 0o755); err != nil {
		return err
	}

	return os.Rename(safeSrc, safeDst)
}

// MkdirAll 处理当前模块的相关逻辑。
func (a *LocalFileSystemAdapter) MkdirAll(path string) error {
	safePath, err := a.policy.EnsureWithinWorkspace(path)
	if err != nil {
		return err
	}

	return os.MkdirAll(safePath, 0o755)
}

// LocalExecutionBackend 定义当前模块的数据结构。
type LocalExecutionBackend struct{}

// Name 处理当前模块的相关逻辑。
func (LocalExecutionBackend) Name() string {
	return "docker"
}

// LocalStorageAdapter 定义当前模块的数据结构。
type LocalStorageAdapter struct {
	databasePath string
}

// NewLocalStorageAdapter 创建并返回LocalStorageAdapter。
func NewLocalStorageAdapter(databasePath string) *LocalStorageAdapter {
	return &LocalStorageAdapter{databasePath: databasePath}
}

// DatabasePath 处理当前模块的相关逻辑。
func (a *LocalStorageAdapter) DatabasePath() string {
	return a.databasePath
}
