// 该文件负责工具注册表与工具元数据。
package tools

// Registry 定义当前模块的数据结构。
type Registry struct {
	toolNames []string
}

// NewRegistry 创建并返回Registry。
func NewRegistry() *Registry {
	return &Registry{toolNames: []string{"read_file", "search_web", "run_command"}}
}

// Names 处理当前模块的相关逻辑。
func (r *Registry) Names() []string {
	return append([]string(nil), r.toolNames...)
}
