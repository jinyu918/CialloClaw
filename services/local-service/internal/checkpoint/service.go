// 该文件负责恢复点层的最小骨架。
package checkpoint

// Service 提供当前模块的服务能力。
type Service struct{}

// NewService 创建并返回Service。
func NewService() *Service {
	return &Service{}
}

// Status 处理当前模块的相关逻辑。
func (s *Service) Status() string {
	return "ready"
}
