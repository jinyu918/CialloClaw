// 该文件负责记忆层接入与检索后端声明。
package memory

// Service 提供当前模块的服务能力。
type Service struct{}

// NewService 创建并返回Service。
func NewService() *Service {
	return &Service{}
}

// RetrievalBackend 处理当前模块的相关逻辑。
func (s *Service) RetrievalBackend() string {
	return "sqlite_fts5+sqlite_vec"
}
