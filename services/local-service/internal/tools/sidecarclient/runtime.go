package sidecarclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/platform"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/plugin"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
)

const sidecarHealthTimeout = 5 * time.Second
const sidecarDefaultTimeout = 20 * time.Second

type workerInvoker interface {
	Invoke(ctx context.Context, request sidecarRequest) (sidecarResponse, error)
}

type sidecarRequest struct {
	Action string `json:"action"`
	URL    string `json:"url,omitempty"`
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type sidecarResponse struct {
	OK     bool              `json:"ok"`
	Result map[string]any    `json:"result,omitempty"`
	Error  *sidecarErrorBody `json:"error,omitempty"`
}

type sidecarErrorBody struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type commandWorkerInvoker struct {
	workdir string
	command string
	args    []string
}

func newCommandWorkerInvoker(workdir string) commandWorkerInvoker {
	return commandWorkerInvoker{
		workdir: workdir,
		command: "node",
		args:    []string{"workers/playwright-worker/src/index.ts"},
	}
}

func (i commandWorkerInvoker) Invoke(ctx context.Context, request sidecarRequest) (sidecarResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return sidecarResponse{}, err
	}
	cmd := exec.CommandContext(ctx, i.command, i.args...)
	if strings.TrimSpace(i.workdir) != "" {
		cmd.Dir = i.workdir
	}
	cmd.Stdin = strings.NewReader(string(payload))
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return sidecarResponse{}, fmt.Errorf("worker command failed: %s", stderr)
			}
		}
		return sidecarResponse{}, err
	}
	var response sidecarResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return sidecarResponse{}, fmt.Errorf("decode worker response: %w", err)
	}
	if !response.OK {
		if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
			return sidecarResponse{}, fmt.Errorf("worker error: %s", response.Error.Message)
		}
		return sidecarResponse{}, errors.New("worker returned failure response")
	}
	return response, nil
}

type runtimePlaywrightClient struct {
	runtime *PlaywrightSidecarRuntime
}

func (c runtimePlaywrightClient) ReadPage(ctx context.Context, url string) (tools.BrowserPageReadResult, error) {
	if c.runtime == nil || !c.runtime.Ready() {
		return tools.BrowserPageReadResult{}, tools.ErrPlaywrightSidecarFailed
	}
	response, err := c.runtime.invoke(ctx, sidecarRequest{Action: "page_read", URL: url})
	if err != nil {
		_ = c.runtime.markFailure()
		return tools.BrowserPageReadResult{}, fmt.Errorf("%w: %v", tools.ErrPlaywrightSidecarFailed, err)
	}
	return tools.BrowserPageReadResult{
		URL:         stringValue(response.Result, "url"),
		Title:       stringValue(response.Result, "title"),
		TextContent: stringValue(response.Result, "text_content"),
		MIMEType:    stringValue(response.Result, "mime_type"),
		TextType:    stringValue(response.Result, "text_type"),
		Source:      firstNonEmptyString(stringValue(response.Result, "source"), "playwright_sidecar"),
	}, nil
}

func (c runtimePlaywrightClient) SearchPage(ctx context.Context, url, query string, limit int) (tools.BrowserPageSearchResult, error) {
	if c.runtime == nil || !c.runtime.Ready() {
		return tools.BrowserPageSearchResult{}, tools.ErrPlaywrightSidecarFailed
	}
	response, err := c.runtime.invoke(ctx, sidecarRequest{Action: "page_search", URL: url, Query: query, Limit: limit})
	if err != nil {
		_ = c.runtime.markFailure()
		return tools.BrowserPageSearchResult{}, fmt.Errorf("%w: %v", tools.ErrPlaywrightSidecarFailed, err)
	}
	return tools.BrowserPageSearchResult{
		URL:        stringValue(response.Result, "url"),
		Query:      stringValue(response.Result, "query"),
		MatchCount: intValue(response.Result, "match_count"),
		Matches:    stringSliceValue(response.Result, "matches"),
		Source:     firstNonEmptyString(stringValue(response.Result, "source"), "playwright_sidecar"),
	}, nil
}

// PlaywrightSidecarRuntime 是当前阶段的最小运行时骨架。
//
// 它负责表达：
// - 当前 sidecar 名称
// - 当前 plugin 声明中是否存在该 sidecar
// - 当前最小 transport 规格
// - 当前 sidecar 是否已进入 ready 状态
type PlaywrightSidecarRuntime struct {
	mu      sync.Mutex
	spec    plugin.SidecarSpec
	os      platform.OSCapabilityAdapter
	ready   bool
	invoker workerInvoker
	client  runtimePlaywrightClient
}

// NewPlaywrightSidecarRuntime 创建并返回最小运行时骨架。
func NewPlaywrightSidecarRuntime(pluginService *plugin.Service, osCapability platform.OSCapabilityAdapter) (*PlaywrightSidecarRuntime, error) {
	spec, ok := pluginService.SidecarSpec("playwright_sidecar")
	if !ok {
		return nil, errors.New("playwright sidecar not declared")
	}
	workdir, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		return nil, err
	}
	runtime := &PlaywrightSidecarRuntime{
		spec:    spec,
		os:      osCapability,
		invoker: newCommandWorkerInvoker(workdir),
	}
	runtime.client = runtimePlaywrightClient{runtime: runtime}
	return runtime, nil
}

// Name 返回当前 sidecar 名称。
func (r *PlaywrightSidecarRuntime) Name() string {
	return r.spec.Name
}

// PipeName 返回当前最小传输骨架使用的命名管道名。
func (r *PlaywrightSidecarRuntime) PipeName() string {
	return sidecarPipeName(r.spec.Name)
}

// Ready 返回当前 sidecar 是否已进入 ready 状态。
func (r *PlaywrightSidecarRuntime) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

// Start 进入当前阶段的最小 ready 状态。
func (r *PlaywrightSidecarRuntime) Start() error {
	if r.os == nil {
		return errors.New("os capability adapter is required")
	}
	if err := r.os.EnsureNamedPipe(sidecarPipeName(r.spec.Name)); err != nil {
		return err
	}
	healthCtx, cancel := context.WithTimeout(context.Background(), sidecarHealthTimeout)
	defer cancel()
	if _, err := r.invoke(healthCtx, sidecarRequest{Action: "health"}); err != nil {
		_ = r.os.CloseNamedPipe(sidecarPipeName(r.spec.Name))
		return fmt.Errorf("%w: %v", tools.ErrPlaywrightSidecarFailed, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = true
	return nil
}

// Stop 退出 ready 状态并关闭最小传输骨架。
func (r *PlaywrightSidecarRuntime) Stop() error {
	if r.os == nil {
		return nil
	}
	if err := r.os.CloseNamedPipe(sidecarPipeName(r.spec.Name)); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = false
	return nil
}

// Client 返回当前运行时关联的最小 client。
func (r *PlaywrightSidecarRuntime) Client() tools.PlaywrightSidecarClient {
	return r.client
}

func (r *PlaywrightSidecarRuntime) invoke(ctx context.Context, request sidecarRequest) (sidecarResponse, error) {
	if r == nil || r.invoker == nil {
		return sidecarResponse{}, errors.New("playwright sidecar invoker is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		boundedCtx, cancel := context.WithTimeout(ctx, sidecarDefaultTimeout)
		defer cancel()
		ctx = boundedCtx
	}
	response, err := r.invoker.Invoke(ctx, request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return sidecarResponse{}, fmt.Errorf("timeout: %w", context.DeadlineExceeded)
		}
		return sidecarResponse{}, err
	}
	return response, nil
}

func (r *PlaywrightSidecarRuntime) markFailure() error {
	r.mu.Lock()
	r.ready = false
	r.mu.Unlock()
	if r.os == nil {
		return nil
	}
	return r.os.CloseNamedPipe(sidecarPipeName(r.spec.Name))
}

func stringValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func intValue(values map[string]any, key string) int {
	if len(values) == 0 {
		return 0
	}
	switch typed := values[key].(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func stringSliceValue(values map[string]any, key string) []string {
	if len(values) == 0 {
		return nil
	}
	raw, ok := values[key].([]any)
	if ok {
		items := make([]string, 0, len(raw))
		for _, item := range raw {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				items = append(items, strings.TrimSpace(value))
			}
		}
		return items
	}
	if typed, ok := values[key].([]string); ok {
		return append([]string(nil), typed...)
	}
	return nil
}

func sidecarPipeName(name string) string {
	return fmt.Sprintf("cialloclaw-%s", name)
}
