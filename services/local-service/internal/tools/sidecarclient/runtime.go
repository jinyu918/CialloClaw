package sidecarclient

import "github.com/cialloclaw/cialloclaw/services/local-service/internal/plugin"

// PlaywrightSidecarRuntime 是当前阶段的最小运行时骨架。
//
// 它不负责真实进程启动，只负责表达：
// - 当前 sidecar 名称
// - 当前 plugin 声明中是否存在该 sidecar
// - 当前 client 获取入口
type PlaywrightSidecarRuntime struct {
	name   string
	plugin *plugin.Service
	client noopPlaywrightSidecarClient
}

// NewPlaywrightSidecarRuntime 创建并返回最小运行时骨架。
func NewPlaywrightSidecarRuntime(pluginService *plugin.Service) *PlaywrightSidecarRuntime {
	return &PlaywrightSidecarRuntime{
		name:   "playwright_sidecar",
		plugin: pluginService,
		client: noopPlaywrightSidecarClient{},
	}
}

// Name 返回当前 sidecar 名称。
func (r *PlaywrightSidecarRuntime) Name() string {
	return r.name
}

// Available 返回当前 plugin 声明中是否具备该 sidecar。
func (r *PlaywrightSidecarRuntime) Available() bool {
	if r.plugin == nil {
		return false
	}
	return r.plugin.HasSidecar(r.name)
}

// Client 返回当前阶段的 sidecar client。
func (r *PlaywrightSidecarRuntime) Client() noopPlaywrightSidecarClient {
	return r.client
}
