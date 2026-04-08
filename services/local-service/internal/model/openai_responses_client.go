// 该文件负责模型接入层的结构或实现。
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// OpenAIResponsesProvider 定义当前模块的基础变量。
const OpenAIResponsesProvider = "openai_responses"

// ErrOpenAIAPIKeyRequired 定义当前模块的基础变量。
var ErrOpenAIAPIKeyRequired = errors.New("openai responses api key is required")
// ErrOpenAIEndpointRequired 定义当前模块的基础变量。
var ErrOpenAIEndpointRequired = errors.New("openai responses endpoint is required")
// ErrOpenAIModelIDRequired 定义当前模块的基础变量。
var ErrOpenAIModelIDRequired = errors.New("openai responses model id is required")
// ErrOpenAIRequestFailed 定义当前模块的基础变量。
var ErrOpenAIRequestFailed = errors.New("openai responses request failed")
// ErrOpenAIRequestTimeout 定义当前模块的基础变量。
var ErrOpenAIRequestTimeout = errors.New("openai responses request timed out")
// ErrOpenAIResponseInvalid 定义当前模块的基础变量。
var ErrOpenAIResponseInvalid = errors.New("openai responses response invalid")
// ErrOpenAIHTTPStatus 定义当前模块的基础变量。
var ErrOpenAIHTTPStatus = errors.New("openai responses http status error")
// ErrGenerateTextInputRequired 定义当前模块的基础变量。
var ErrGenerateTextInputRequired = errors.New("generate text input is required")

// OpenAIResponsesClientConfig 描述当前模块配置。
type OpenAIResponsesClientConfig struct {
	APIKey     string
	Endpoint   string
	ModelID    string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// OpenAIResponsesClient 定义当前模块的数据结构。
type OpenAIResponsesClient struct {
	apiKey     string
	endpoint   string
	modelID    string
	timeout    time.Duration
	httpClient *http.Client
}

// defaultOpenAIResponsesTimeout 定义当前模块的基础变量。
const defaultOpenAIResponsesTimeout = 30 * time.Second

// openAIResponsesGenerateRequest 描述当前模块请求结构。
type openAIResponsesGenerateRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// openAIResponsesGenerateResponse 定义当前模块的数据结构。
type openAIResponsesGenerateResponse struct {
	ID         string                       `json:"id"`
	Model      string                       `json:"model"`
	OutputText string                       `json:"output_text"`
	Output     []openAIResponsesOutputItem  `json:"output"`
	Usage      openAIResponsesUsage         `json:"usage"`
	Error      *openAIResponsesErrorPayload `json:"error"`
}

// openAIResponsesOutputItem 定义当前模块的数据结构。
type openAIResponsesOutputItem struct {
	Content []openAIResponsesContentItem `json:"content"`
}

// openAIResponsesContentItem 定义当前模块的数据结构。
type openAIResponsesContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// openAIResponsesUsage 定义当前模块的数据结构。
type openAIResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// openAIResponsesErrorPayload 定义当前模块的数据结构。
type openAIResponsesErrorPayload struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// OpenAIHTTPStatusError 定义当前模块的数据结构。
type OpenAIHTTPStatusError struct {
	StatusCode int
	Message    string
}

// Error 处理当前模块的相关逻辑。
func (e *OpenAIHTTPStatusError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("openai responses returned http status %d", e.StatusCode)
	}

	return fmt.Sprintf("openai responses returned http status %d: %s", e.StatusCode, e.Message)
}

// Unwrap 处理当前模块的相关逻辑。
func (e *OpenAIHTTPStatusError) Unwrap() error {
	return ErrOpenAIHTTPStatus
}

// NewOpenAIResponsesClient 创建并返回OpenAIResponsesClient。
func NewOpenAIResponsesClient(cfg OpenAIResponsesClientConfig) (*OpenAIResponsesClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, ErrOpenAIAPIKeyRequired
	}

	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, ErrOpenAIEndpointRequired
	}

	if strings.TrimSpace(cfg.ModelID) == "" {
		return nil, ErrOpenAIModelIDRequired
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultOpenAIResponsesTimeout
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else {
		clonedClient := *httpClient
		if clonedClient.Timeout <= 0 {
			clonedClient.Timeout = timeout
		}
		httpClient = &clonedClient
	}

	return &OpenAIResponsesClient{
		apiKey:     cfg.APIKey,
		endpoint:   cfg.Endpoint,
		modelID:    cfg.ModelID,
		timeout:    timeout,
		httpClient: httpClient,
	}, nil
}

// GenerateText 处理当前模块的相关逻辑。
func (c *OpenAIResponsesClient) GenerateText(ctx context.Context, request GenerateTextRequest) (GenerateTextResponse, error) {
	startedAt := time.Now()
	if strings.TrimSpace(request.Input) == "" {
		return GenerateTextResponse{}, ErrGenerateTextInputRequired
	}

	payload := openAIResponsesGenerateRequest{
		Model: c.modelID,
		Input: request.Input,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return GenerateTextResponse{}, fmt.Errorf("marshal openai responses request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return GenerateTextResponse{}, fmt.Errorf("create openai responses request: %w", err)
	}

	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return GenerateTextResponse{}, classifyOpenAIRequestError(err)
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return GenerateTextResponse{}, fmt.Errorf("read openai responses response body: %w", err)
	}

	parsedResponse, err := parseOpenAIResponsesGenerateResponse(httpResponse.StatusCode, responseBody)
	if err != nil {
		return GenerateTextResponse{}, err
	}

	return GenerateTextResponse{
		TaskID:     request.TaskID,
		RunID:      request.RunID,
		RequestID:  parsedResponse.ID,
		Provider:   OpenAIResponsesProvider,
		ModelID:    firstNonEmpty(parsedResponse.Model, c.modelID),
		OutputText: extractOpenAIOutputText(parsedResponse),
		Usage: TokenUsage{
			InputTokens:  parsedResponse.Usage.InputTokens,
			OutputTokens: parsedResponse.Usage.OutputTokens,
			TotalTokens:  parsedResponse.Usage.TotalTokens,
		},
		LatencyMS: time.Since(startedAt).Milliseconds(),
	}, nil
}

// Provider 处理当前模块的相关逻辑。
func (c *OpenAIResponsesClient) Provider() string {
	return OpenAIResponsesProvider
}

// ModelID 处理当前模块的相关逻辑。
func (c *OpenAIResponsesClient) ModelID() string {
	return c.modelID
}

// Endpoint 处理当前模块的相关逻辑。
func (c *OpenAIResponsesClient) Endpoint() string {
	return c.endpoint
}

// parseOpenAIResponsesGenerateResponse 处理当前模块的相关逻辑。
func parseOpenAIResponsesGenerateResponse(statusCode int, body []byte) (openAIResponsesGenerateResponse, error) {
	var parsed openAIResponsesGenerateResponse

	if statusCode < 200 || statusCode >= 300 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			return openAIResponsesGenerateResponse{}, &OpenAIHTTPStatusError{
				StatusCode: statusCode,
				Message:    truncateErrorMessage(string(body)),
			}
		}

		message := ""
		if parsed.Error != nil {
			message = parsed.Error.Message
		}

		return openAIResponsesGenerateResponse{}, &OpenAIHTTPStatusError{
			StatusCode: statusCode,
			Message:    message,
		}
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return openAIResponsesGenerateResponse{}, fmt.Errorf("%w: %v", ErrOpenAIResponseInvalid, err)
	}

	return parsed, nil
}

// extractOpenAIOutputText 处理当前模块的相关逻辑。
func extractOpenAIOutputText(response openAIResponsesGenerateResponse) string {
	if strings.TrimSpace(response.OutputText) != "" {
		return response.OutputText
	}

	var builder strings.Builder
	for _, item := range response.Output {
		for _, content := range item.Content {
			if content.Type != "output_text" && content.Type != "text" {
				continue
			}

			builder.WriteString(content.Text)
		}
	}

	return builder.String()
}

// classifyOpenAIRequestError 处理当前模块的相关逻辑。
func classifyOpenAIRequestError(err error) error {
	if err == nil {
		return nil
	}

	if isOpenAITimeoutError(err) {
		return fmt.Errorf("%w: %v", ErrOpenAIRequestTimeout, err)
	}

	return fmt.Errorf("%w: %v", ErrOpenAIRequestFailed, err)
}

// isOpenAITimeoutError 处理当前模块的相关逻辑。
func isOpenAITimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// firstNonEmpty 处理当前模块的相关逻辑。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

// truncateErrorMessage 处理当前模块的相关逻辑。
func truncateErrorMessage(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 256 {
		return trimmed
	}

	return trimmed[:256]
}
