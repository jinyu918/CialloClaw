// 该文件负责模型接入层的结构或实现。
package model

import (
	"context"
	"errors"
	"strings"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/config"
)

// Service 提供当前模块的服务能力。
type Service struct {
	provider string
	modelID  string
	endpoint string
	client   Client
}

// ErrClientNotConfigured 定义当前模块的基础变量。
var ErrClientNotConfigured = errors.New("model client not configured")
// ErrModelProviderRequired 定义当前模块的基础变量。
var ErrModelProviderRequired = errors.New("model provider is required")
// ErrModelProviderUnsupported 定义当前模块的基础变量。
var ErrModelProviderUnsupported = errors.New("model provider unsupported")

// ServiceConfig 描述当前模块配置。
type ServiceConfig struct {
	ModelConfig config.ModelConfig
	APIKey      string
}

// NewService 创建并返回Service。
func NewService(cfg config.ModelConfig, clients ...Client) *Service {
	var client Client
	if len(clients) > 0 {
		client = clients[0]
	}

	return &Service{
		provider: cfg.Provider,
		modelID:  cfg.ModelID,
		endpoint: cfg.Endpoint,
		client:   client,
	}
}

// NewServiceFromConfig 创建并返回ServiceFromConfig。
func NewServiceFromConfig(cfg ServiceConfig) (*Service, error) {
	if err := ValidateModelConfig(cfg.ModelConfig); err != nil {
		return nil, err
	}

	client, err := buildClient(cfg)
	if err != nil {
		return nil, err
	}

	return NewService(cfg.ModelConfig, client), nil
}

// Descriptor 处理当前模块的相关逻辑。
func (s *Service) Descriptor() string {
	return s.provider + ":" + s.modelID
}

// Provider 处理当前模块的相关逻辑。
func (s *Service) Provider() string {
	return s.provider
}

// ModelID 处理当前模块的相关逻辑。
func (s *Service) ModelID() string {
	return s.modelID
}

// Endpoint 处理当前模块的相关逻辑。
func (s *Service) Endpoint() string {
	return s.endpoint
}

// GenerateText 处理当前模块的相关逻辑。
func (s *Service) GenerateText(ctx context.Context, request GenerateTextRequest) (GenerateTextResponse, error) {
	if s.client == nil {
		return GenerateTextResponse{}, ErrClientNotConfigured
	}

	return s.client.GenerateText(ctx, request)
}

// ValidateModelConfig 处理当前模块的相关逻辑。
func ValidateModelConfig(cfg config.ModelConfig) error {
	provider := strings.TrimSpace(cfg.Provider)
	endpoint := strings.TrimSpace(cfg.Endpoint)
	modelID := strings.TrimSpace(cfg.ModelID)

	if provider == "" {
		return ErrModelProviderRequired
	}

	switch provider {
	case OpenAIResponsesProvider:
		if endpoint == "" {
			return ErrOpenAIEndpointRequired
		}
		if modelID == "" {
			return ErrOpenAIModelIDRequired
		}
		return nil
	default:
		return ErrModelProviderUnsupported
	}
}

// buildClient 处理当前模块的相关逻辑。
func buildClient(cfg ServiceConfig) (Client, error) {
	switch strings.TrimSpace(cfg.ModelConfig.Provider) {
	case OpenAIResponsesProvider:
		return NewOpenAIResponsesClient(OpenAIResponsesClientConfig{
			APIKey:   cfg.APIKey,
			Endpoint: strings.TrimSpace(cfg.ModelConfig.Endpoint),
			ModelID:  strings.TrimSpace(cfg.ModelConfig.ModelID),
		})
	default:
		return nil, ErrModelProviderUnsupported
	}
}
