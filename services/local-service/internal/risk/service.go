// 该文件负责风险评估层的最小骨架。
package risk

// Service 提供当前模块的服务能力。
type Service struct{}

// NewService 创建并返回Service。
func NewService() *Service {
	return &Service{}
}

// DefaultLevel 处理当前模块的相关逻辑。
func (s *Service) DefaultLevel() string {
	return "green"
}
