import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Badge, Button, Heading, SegmentedControl, Slider, Switch, Text, TextArea, TextField } from "@radix-ui/themes";
import {
  Bell,
  Cpu,
  Database,
  PlayCircle,
  ShieldCheck,
  Sparkles,
  WandSparkles,
  type LucideIcon,
} from "lucide-react";
import {
  loadControlPanelData,
  runControlPanelInspection,
  saveControlPanelData,
  type ControlPanelData,
} from "@/services/controlPanelService";

type SectionKey = "general" | "floating_ball" | "memory" | "task_automation" | "data_log";

type SectionDefinition = {
  key: SectionKey;
  label: string;
  eyebrow: string;
  title: string;
  description: string;
  icon: LucideIcon;
};

type StickerFieldProps = {
  label: string;
  hint: string;
  children: ReactNode;
};

type StickerSwitchProps = {
  label: string;
  description: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
};

type BubbleMetricProps = {
  label: string;
  value: string;
  detail: string;
};

const SECTION_ORDER: SectionKey[] = ["general", "floating_ball", "memory", "task_automation", "data_log"];

const SECTION_DEFINITIONS: Record<SectionKey, SectionDefinition> = {
  general: {
    key: "general",
    label: "桌面",
    eyebrow: "workspace basics",
    title: "桌面基底",
    description: "把语言、工作区和启动偏好放进一张更平静的桌面基底里。",
    icon: Sparkles,
  },
  floating_ball: {
    key: "floating_ball",
    label: "悬浮球",
    eyebrow: "floating anchor",
    title: "悬浮球停靠",
    description: "贴边、透明度和尺寸仍走原有字段，只是用更柔软的方式整理出来。",
    icon: WandSparkles,
  },
  memory: {
    key: "memory",
    label: "记忆",
    eyebrow: "memory rhythm",
    title: "记忆节奏",
    description: "主开关、生命周期与刷新节奏保持原逻辑，阅读层次更轻。",
    icon: Database,
  },
  task_automation: {
    key: "task_automation",
    label: "巡检",
    eyebrow: "inspection cadence",
    title: "巡检节奏",
    description: "保留巡检配置、手动触发和 inspector 双写路径，只把操作墙拆得更松。",
    icon: Cpu,
  },
  data_log: {
    key: "data_log",
    label: "预算",
    eyebrow: "budget route",
    title: "预算与路由",
    description: "Provider、自动降级和安全摘要继续沿用现有来源，只换成更圆润的承载面。",
    icon: Bell,
  },
};

function getSourceCopy(source: ControlPanelData["source"]) {
  if (source === "rpc") {
    return {
      badge: "LIVE",
      title: "当前显示的是 JSON-RPC 实时配置数据",
      description: "来自后端返回，可直接验证控制面板联调。",
      color: "green" as const,
      className: "control-panel-page__status-card--rpc",
    };
  }

  return {
    badge: "MOCK",
    title: "当前显示的是本地设置快照",
    description: "仅用于前端联调，不是真实后端返回。",
    color: "amber" as const,
    className: "control-panel-page__status-card--mock",
  };
}

function getApplyModeCopy(applyMode: string, needRestart: boolean) {
  if (needRestart) {
    return "部分设置需要重启桌面端后生效。";
  }

  if (applyMode === "next_task_effective") {
    return "设置已保存，将在下一个任务周期生效。";
  }

  return "设置已即时生效。";
}

function formatThemeMode(value: string) {
  if (value === "follow_system") {
    return "跟随系统";
  }

  if (value === "dark") {
    return "深色";
  }

  return "浅色";
}

function formatFloatingBallSize(value: string) {
  if (value === "small") {
    return "小";
  }

  if (value === "large") {
    return "大";
  }

  return "中";
}

function formatPositionMode(value: string) {
  if (value === "fixed") {
    return "固定";
  }

  return "拖动";
}

function getSectionSummary(section: SectionKey, draft: ControlPanelData) {
  if (section === "general") {
    return [`语言 ${draft.settings.general.language}`, `主题 ${formatThemeMode(draft.settings.general.theme_mode)}`];
  }

  if (section === "floating_ball") {
    return [
      `尺寸 ${formatFloatingBallSize(draft.settings.floating_ball.size)}`,
      `模式 ${formatPositionMode(draft.settings.floating_ball.position_mode)}`,
    ];
  }

  if (section === "memory") {
    return [draft.settings.memory.enabled ? "记忆已开启" : "记忆已关闭", draft.settings.memory.lifecycle];
  }

  if (section === "task_automation") {
    return [
      `${draft.inspector.task_sources.length} 个来源`,
      `每 ${draft.inspector.inspection_interval.value}${draft.inspector.inspection_interval.unit}`,
    ];
  }

  return [
    draft.settings.data_log.provider,
    draft.settings.data_log.budget_auto_downgrade ? "自动降级开启" : "自动降级关闭",
  ];
}

function StickerField({ label, hint, children }: StickerFieldProps) {
  return (
    <div className="control-panel-page__field-card">
      <div className="control-panel-page__field-copy">
        <Text as="p" size="1" className="control-panel-page__micro-label">
          {label}
        </Text>
        <Text as="p" size="2" className="control-panel-page__field-hint">
          {hint}
        </Text>
      </div>
      <div className="control-panel-page__control-wrap">{children}</div>
    </div>
  );
}

function StickerSwitch({ label, description, checked, onCheckedChange }: StickerSwitchProps) {
  return (
    <div className="control-panel-page__toggle-card">
      <div className="control-panel-page__toggle-copy">
        <Text as="p" size="2" weight="medium" className="control-panel-page__toggle-title">
          {label}
        </Text>
        <Text as="p" size="1" className="control-panel-page__toggle-description">
          {description}
        </Text>
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  );
}

function BubbleMetric({ label, value, detail }: BubbleMetricProps) {
  return (
    <article className="control-panel-page__metric-bubble">
      <Text as="p" size="1" className="control-panel-page__micro-label">
        {label}
      </Text>
      <Text as="p" size="5" className="control-panel-page__metric-value">
        {value}
      </Text>
      <Text as="p" size="1" className="control-panel-page__metric-detail">
        {detail}
      </Text>
    </article>
  );
}

export function ControlPanelApp() {
  const [panelData, setPanelData] = useState<ControlPanelData | null>(null);
  const [draft, setDraft] = useState<ControlPanelData | null>(null);
  const [activeSection, setActiveSection] = useState<SectionKey>("general");
  const [saveFeedback, setSaveFeedback] = useState<string | null>(null);
  const [inspectionSummary, setInspectionSummary] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const [isRunningInspection, setIsRunningInspection] = useState(false);

  useEffect(() => {
    void loadControlPanelData().then((nextData) => {
      setPanelData(nextData);
      setDraft(nextData);
    });
  }, []);

  const sourceCopy = useMemo(() => (draft ? getSourceCopy(draft.source) : null), [draft]);

  if (!draft || !panelData || !sourceCopy) {
    return (
      <main className="app-shell control-panel-page">
        <div className="control-panel-page__loading">
          <Text size="2" className="control-panel-page__loading-copy">
            正在整理控制面板工作面…
          </Text>
        </div>
      </main>
    );
  }

  const hasChanges = JSON.stringify(draft) !== JSON.stringify(panelData);
  const sectionDefinition = SECTION_DEFINITIONS[activeSection];
  const SectionIcon = sectionDefinition.icon;
  const sectionSummary = getSectionSummary(activeSection, draft);
  const latestRestorePoint = draft.securitySummary.latest_restore_point?.summary ?? "暂无恢复点";
  const inspectionInterval = `${draft.inspector.inspection_interval.value}${draft.inspector.inspection_interval.unit}`;
  const memoryCadence = `工作总结：${draft.settings.memory.work_summary_interval.value}${draft.settings.memory.work_summary_interval.unit}\n用户画像：${draft.settings.memory.profile_refresh_interval.value}${draft.settings.memory.profile_refresh_interval.unit}`;

  const updateSettings = (updater: (current: ControlPanelData) => ControlPanelData) => {
    setDraft((current) => (current ? updater(current) : current));
  };

  const handleSave = async () => {
    setIsSaving(true);
    try {
      const result = await saveControlPanelData(draft);
      const nextData: ControlPanelData = {
        ...draft,
        settings: {
          ...draft.settings,
          ...result.effectiveSettings,
          task_automation: {
            ...draft.settings.task_automation,
            ...(result.effectiveSettings.task_automation ?? {}),
          },
        },
      };
      setPanelData(nextData);
      setDraft(nextData);
      setSaveFeedback(getApplyModeCopy(result.applyMode, result.needRestart));
    } catch (error) {
      setSaveFeedback(error instanceof Error ? error.message : "保存控制面板设置失败。");
    } finally {
      setIsSaving(false);
    }
  };

  const handleRunInspection = async () => {
    setIsRunningInspection(true);
    try {
      const result = await runControlPanelInspection(draft);
      setInspectionSummary(
        `本次巡检解析 ${result.summary.parsed_files} 个文件，识别 ${result.summary.identified_items} 条事项，逾期 ${result.summary.overdue} 条。`,
      );
    } catch (error) {
      setInspectionSummary(error instanceof Error ? error.message : "手动巡检执行失败。");
    } finally {
      setIsRunningInspection(false);
    }
  };

  const renderWorkspace = () => {
    if (activeSection === "general") {
      return (
        <div className="control-panel-page__scene">
          <article className="control-panel-page__surface control-panel-page__surface--main control-panel-page__surface--blush">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                workspace basics
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                桌面语言与路径
              </Heading>
              <Text as="p" size="2" className="control-panel-page__surface-copy">
                最常改的基础信息先落在主工作面里，进入后就能直接编辑。
              </Text>
            </div>

            <div className="control-panel-page__surface-grid">
              <StickerField label="语言" hint="启动与全局交互的显示语言。">
                <TextField.Root
                  className="control-panel-page__input"
                  value={draft.settings.general.language}
                  onChange={(event) =>
                    updateSettings((current) => ({
                      ...current,
                      settings: {
                        ...current.settings,
                        general: { ...current.settings.general, language: event.target.value },
                      },
                    }))
                  }
                />
              </StickerField>

              <StickerField label="工作区路径" hint="默认下载与任务落盘位置。">
                <TextField.Root
                  className="control-panel-page__input"
                  value={draft.settings.general.download.workspace_path}
                  onChange={(event) =>
                    updateSettings((current) => ({
                      ...current,
                      settings: {
                        ...current.settings,
                        general: {
                          ...current.settings.general,
                          download: { ...current.settings.general.download, workspace_path: event.target.value },
                        },
                      },
                    }))
                  }
                />
              </StickerField>
            </div>
          </article>

          <article className="control-panel-page__surface control-panel-page__surface--secondary control-panel-page__surface--warm">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                startup rhythm
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                启动习惯
              </Heading>
            </div>

            <div className="control-panel-page__toggle-stack">
              <StickerSwitch
                label="开机自启"
                description="桌面宿主启动时自动进入当前工作台。"
                checked={draft.settings.general.auto_launch}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    settings: {
                      ...current.settings,
                      general: { ...current.settings.general, auto_launch: checked },
                    },
                  }))
                }
              />

              <StickerSwitch
                label="语音通知"
                description="任务状态变化时保留语音播报提醒。"
                checked={draft.settings.general.voice_notification_enabled}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    settings: {
                      ...current.settings,
                      general: { ...current.settings.general, voice_notification_enabled: checked },
                    },
                  }))
                }
              />
            </div>
          </article>

          <aside className="control-panel-page__surface control-panel-page__surface--aside control-panel-page__surface--mist">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                appearance
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                主题模式
              </Heading>
              <Text as="p" size="2" className="control-panel-page__surface-copy">
                控制面板保持明亮柔和，主题偏好仍写回原有配置结构。
              </Text>
            </div>

            <SegmentedControl.Root
              value={draft.settings.general.theme_mode}
              onValueChange={(value) =>
                updateSettings((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    general: { ...current.settings.general, theme_mode: value as typeof current.settings.general.theme_mode },
                  },
                }))
              }
              className="control-panel-page__selector"
            >
              <SegmentedControl.Item value="follow_system">跟随系统</SegmentedControl.Item>
              <SegmentedControl.Item value="light">浅色</SegmentedControl.Item>
              <SegmentedControl.Item value="dark">深色</SegmentedControl.Item>
            </SegmentedControl.Root>

            <div className="control-panel-page__surface-note">当前偏好：{formatThemeMode(draft.settings.general.theme_mode)}。</div>
          </aside>
        </div>
      );
    }

    if (activeSection === "floating_ball") {
      return (
        <div className="control-panel-page__scene">
          <article className="control-panel-page__surface control-panel-page__surface--main control-panel-page__surface--blush">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                edge behavior
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                贴边动作
              </Heading>
              <Text as="p" size="2" className="control-panel-page__surface-copy">
                常改动作留在同一块缓和的校准面里，减少切换负担。
              </Text>
            </div>

            <div className="control-panel-page__toggle-stack">
              <StickerSwitch
                label="自动贴边"
                description="悬浮球靠近屏幕边缘时自动吸附。"
                checked={draft.settings.floating_ball.auto_snap}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    settings: {
                      ...current.settings,
                      floating_ball: { ...current.settings.floating_ball, auto_snap: checked },
                    },
                  }))
                }
              />

              <StickerSwitch
                label="空闲半透明"
                description="闲置时降低存在感，保持桌面更轻。"
                checked={draft.settings.floating_ball.idle_translucent}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    settings: {
                      ...current.settings,
                      floating_ball: { ...current.settings.floating_ball, idle_translucent: checked },
                    },
                  }))
                }
              />
            </div>
          </article>

          <article className="control-panel-page__surface control-panel-page__surface--secondary control-panel-page__surface--warm">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                size dial
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                尺寸调节
              </Heading>
            </div>

            <StickerField label="尺寸" hint={`当前为 ${formatFloatingBallSize(draft.settings.floating_ball.size)} 档。`}>
              <div className="control-panel-page__slider-stack">
                <Slider
                  min={0}
                  max={2}
                  step={1}
                  value={[
                    draft.settings.floating_ball.size === "small"
                      ? 0
                      : draft.settings.floating_ball.size === "medium"
                        ? 1
                        : 2,
                  ]}
                  onValueChange={([value]) =>
                    updateSettings((current) => ({
                      ...current,
                      settings: {
                        ...current.settings,
                        floating_ball: {
                          ...current.settings.floating_ball,
                          size: value === 0 ? "small" : value === 1 ? "medium" : "large",
                        },
                      },
                    }))
                  }
                />
                <div className="control-panel-page__slider-legend">
                  <span>小</span>
                  <span>中</span>
                  <span>大</span>
                </div>
              </div>
            </StickerField>
          </article>

          <aside className="control-panel-page__surface control-panel-page__surface--aside control-panel-page__surface--mist">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                anchor mode
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                停靠方式
              </Heading>
            </div>

            <SegmentedControl.Root
              value={draft.settings.floating_ball.position_mode}
              onValueChange={(value) =>
                updateSettings((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    floating_ball: {
                      ...current.settings.floating_ball,
                      position_mode: value as typeof current.settings.floating_ball.position_mode,
                    },
                  },
                }))
              }
              className="control-panel-page__selector"
            >
              <SegmentedControl.Item value="fixed">固定</SegmentedControl.Item>
              <SegmentedControl.Item value="draggable">拖动</SegmentedControl.Item>
            </SegmentedControl.Root>

            <div className="control-panel-page__surface-note">
              当前以 {formatPositionMode(draft.settings.floating_ball.position_mode)} 模式运行，尺寸为 {formatFloatingBallSize(draft.settings.floating_ball.size)} 档。
            </div>
          </aside>
        </div>
      );
    }

    if (activeSection === "memory") {
      return (
        <div className="control-panel-page__scene">
          <article className="control-panel-page__surface control-panel-page__surface--main control-panel-page__surface--blush">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                master memory
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                记忆开关
              </Heading>
              <Text as="p" size="2" className="control-panel-page__surface-copy">
                把是否参与主链路放在前面，一眼就能确认当前状态。
              </Text>
            </div>

            <div className="control-panel-page__toggle-stack">
              <StickerSwitch
                label="记忆主开关"
                description="控制记忆能力是否参与桌面主链路。"
                checked={draft.settings.memory.enabled}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    settings: {
                      ...current.settings,
                      memory: { ...current.settings.memory, enabled: checked },
                    },
                  }))
                }
              />
            </div>
          </article>

          <article className="control-panel-page__surface control-panel-page__surface--secondary control-panel-page__surface--warm">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                lifecycle note
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                生命周期
              </Heading>
            </div>

            <StickerField label="生命周期" hint="用于描述记忆轮转或保留周期。">
              <TextField.Root
                className="control-panel-page__input"
                value={draft.settings.memory.lifecycle}
                onChange={(event) =>
                  updateSettings((current) => ({
                    ...current,
                    settings: {
                      ...current.settings,
                      memory: { ...current.settings.memory, lifecycle: event.target.value },
                    },
                  }))
                }
              />
            </StickerField>
          </article>

          <aside className="control-panel-page__surface control-panel-page__surface--aside control-panel-page__surface--mist">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                cadence snapshot
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                刷新节奏
              </Heading>
            </div>

            <TextArea className="control-panel-page__textarea control-panel-page__textarea--compact" value={memoryCadence} readOnly />

            <div className="control-panel-page__surface-note">这里仅调整展示方式，不改刷新字段和回退行为。</div>
          </aside>
        </div>
      );
    }

    if (activeSection === "task_automation") {
      return (
        <div className="control-panel-page__scene">
          <article className="control-panel-page__surface control-panel-page__surface--main control-panel-page__surface--blush">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                automation toggles
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                巡检触发
              </Heading>
              <Text as="p" size="2" className="control-panel-page__surface-copy">
                高频开关保留在同一层里，编辑时更容易连续对照。
              </Text>
            </div>

            <div className="control-panel-page__toggle-stack control-panel-page__toggle-stack--two-columns">
              <StickerSwitch
                label="开机巡检"
                description="桌面端启动后自动拉起一次巡检。"
                checked={draft.inspector.inspect_on_startup}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    inspector: { ...current.inspector, inspect_on_startup: checked },
                    settings: {
                      ...current.settings,
                      task_automation: { ...current.settings.task_automation, inspect_on_startup: checked },
                    },
                  }))
                }
              />

              <StickerSwitch
                label="文件变化时巡检"
                description="检测来源变化后，自动发起新一轮巡检。"
                checked={draft.inspector.inspect_on_file_change}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    inspector: { ...current.inspector, inspect_on_file_change: checked },
                    settings: {
                      ...current.settings,
                      task_automation: { ...current.settings.task_automation, inspect_on_file_change: checked },
                    },
                  }))
                }
              />

              <StickerSwitch
                label="截止前提醒"
                description="任务临近截止时间时主动提醒。"
                checked={draft.inspector.remind_before_deadline}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    inspector: { ...current.inspector, remind_before_deadline: checked },
                    settings: {
                      ...current.settings,
                      task_automation: { ...current.settings.task_automation, remind_before_deadline: checked },
                    },
                  }))
                }
              />

              <StickerSwitch
                label="陈旧任务提醒"
                description="长时间没有推进的任务会进入提醒队列。"
                checked={draft.inspector.remind_when_stale}
                onCheckedChange={(checked) =>
                  updateSettings((current) => ({
                    ...current,
                    inspector: { ...current.inspector, remind_when_stale: checked },
                    settings: {
                      ...current.settings,
                      task_automation: { ...current.settings.task_automation, remind_when_stale: checked },
                    },
                  }))
                }
              />
            </div>
          </article>

          <article className="control-panel-page__surface control-panel-page__surface--secondary control-panel-page__surface--warm">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                source list
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                来源列表
              </Heading>
            </div>

            <StickerField label="任务来源" hint="每行一个路径或来源标签，仍会同步到 inspector 和 settings 双份结构。">
              <TextArea
                className="control-panel-page__textarea"
                value={draft.inspector.task_sources.join("\n")}
                onChange={(event) =>
                  updateSettings((current) => {
                    const taskSources = event.target.value
                      .split(/\r?\n/)
                      .map((item) => item.trim())
                      .filter(Boolean);

                    return {
                      ...current,
                      inspector: { ...current.inspector, task_sources: taskSources },
                      settings: {
                        ...current.settings,
                        task_automation: { ...current.settings.task_automation, task_sources: taskSources },
                      },
                    };
                  })
                }
              />
            </StickerField>
          </article>

          <aside className="control-panel-page__surface control-panel-page__surface--aside control-panel-page__surface--mist">
            <div className="control-panel-page__surface-head">
              <Text as="p" size="1" className="control-panel-page__micro-label">
                inspection rhythm
              </Text>
              <Heading size="4" className="control-panel-page__surface-title">
                巡检摘要
              </Heading>
            </div>

            <div className="control-panel-page__surface-note">巡检频率为 {inspectionInterval}，来源数 {draft.inspector.task_sources.length} 个。</div>
            <div className="control-panel-page__surface-note">手动巡检按钮保留在右侧操作区，继续沿用当前 RPC / fallback 逻辑。</div>
          </aside>
        </div>
      );
    }

    return (
      <div className="control-panel-page__scene">
        <article className="control-panel-page__surface control-panel-page__surface--main control-panel-page__surface--blush">
          <div className="control-panel-page__surface-head">
            <Text as="p" size="1" className="control-panel-page__micro-label">
              provider route
            </Text>
            <Heading size="4" className="control-panel-page__surface-title">
              模型路由
            </Heading>
            <Text as="p" size="2" className="control-panel-page__surface-copy">
              Provider 名称单独放在主卡片中，便于快速确认当前落点。
            </Text>
          </div>

          <StickerField label="Provider" hint="当前数据日志与预算模块使用的 Provider 标识。">
            <TextField.Root
              className="control-panel-page__input"
              value={draft.settings.data_log.provider}
              onChange={(event) =>
                updateSettings((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    data_log: { ...current.settings.data_log, provider: event.target.value },
                  },
                }))
              }
            />
          </StickerField>
        </article>

        <article className="control-panel-page__surface control-panel-page__surface--secondary control-panel-page__surface--warm">
          <div className="control-panel-page__surface-head">
            <Text as="p" size="1" className="control-panel-page__micro-label">
              degrade switch
            </Text>
            <Heading size="4" className="control-panel-page__surface-title">
              预算缓冲
            </Heading>
          </div>

          <div className="control-panel-page__toggle-stack">
            <StickerSwitch
              label="预算自动降级"
              description="达到预算阈值后继续沿用现有降级策略。"
              checked={draft.settings.data_log.budget_auto_downgrade}
              onCheckedChange={(checked) =>
                updateSettings((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    data_log: { ...current.settings.data_log, budget_auto_downgrade: checked },
                  },
                }))
              }
            />
          </div>
        </article>

        <aside className="control-panel-page__surface control-panel-page__surface--aside control-panel-page__surface--mist">
          <div className="control-panel-page__surface-head">
            <Text as="p" size="1" className="control-panel-page__micro-label">
              budget pocket
            </Text>
            <Heading size="4" className="control-panel-page__surface-title">
              实时摘要
            </Heading>
          </div>

          <div className="control-panel-page__console-card">
            <Text as="p" size="1" className="control-panel-page__console-line">
              security_status: {draft.securitySummary.security_status}
            </Text>
            <Text as="p" size="1" className="control-panel-page__console-line">
              pending_authorizations: {draft.securitySummary.pending_authorizations}
            </Text>
            <Text as="p" size="1" className="control-panel-page__console-line">
              today_cost: ¥{draft.securitySummary.token_cost_summary.today_cost.toFixed(2)}
            </Text>
          </div>

          <div className="control-panel-page__surface-note">
            预算摘要继续直接来自 <code>securitySummary</code>，因此 fallback / RPC 行为保持不变。
          </div>
        </aside>
      </div>
    );
  };

  return (
    <main className="app-shell control-panel-page">
      <div className="control-panel-page__frame">
        <header className="control-panel-page__hero">
          <div className="control-panel-page__hero-copy">
            <div className="control-panel-page__hero-chip">
              <Sparkles className="control-panel-page__hero-chip-icon" />
              <span>local control surface</span>
            </div>

            <Heading size="7" className="control-panel-page__hero-title">
              控制面板
            </Heading>

            <Text as="p" size="2" className="control-panel-page__hero-text">
              把桌面设置、巡检与预算收进一块更柔和的系统工作面里；保存流程、巡检动作、入口 wiring 与 RPC / fallback 行为保持原样。
            </Text>
          </div>

          <div className="control-panel-page__hero-side">
            <aside className={`control-panel-page__status-card ${sourceCopy.className}`} aria-label="控制面板数据来源状态">
              <Badge color={sourceCopy.color} variant="soft" highContrast className="w-fit">
                {sourceCopy.badge}
              </Badge>
              <div className="control-panel-page__status-copy">
                <Text as="p" size="2" weight="medium" className="control-panel-page__status-title">
                  {sourceCopy.title}
                </Text>
                <Text as="p" size="1" className="control-panel-page__status-description">
                  {sourceCopy.description}
                </Text>
              </div>
            </aside>

            <div className="control-panel-page__hero-metrics" aria-label="控制面板关键状态总览">
              <BubbleMetric
                label="save state"
                value={hasChanges ? "待保存" : "已同步"}
                detail={hasChanges ? "当前存在未保存修改。" : "草稿与生效配置一致。"}
              />
              <BubbleMetric
                label="pending approvals"
                value={`${draft.securitySummary.pending_authorizations}`}
                detail="安全模块当前待确认授权数。"
              />
              <BubbleMetric
                label="today cost"
                value={`¥${draft.securitySummary.token_cost_summary.today_cost.toFixed(2)}`}
                detail="今日 token 成本摘要。"
              />
            </div>
          </div>
        </header>

        <div className="control-panel-page__workspace">
          <nav className="control-panel-page__section-rail" aria-label="控制面板分区切换">
            {SECTION_ORDER.map((sectionKey) => {
              const definition = SECTION_DEFINITIONS[sectionKey];
              const Icon = definition.icon;
              const summary = getSectionSummary(sectionKey, draft).join(" · ");
              const isActive = sectionKey === activeSection;

              return (
                <button
                  key={sectionKey}
                  type="button"
                  data-section={sectionKey}
                  aria-pressed={isActive}
                  className={`control-panel-page__section-tab ${isActive ? "control-panel-page__section-tab--active" : ""}`}
                  onClick={() => setActiveSection(sectionKey)}
                >
                  <span className="control-panel-page__section-tab-icon-shell">
                    <Icon className="control-panel-page__section-tab-icon" />
                  </span>
                  <span className="control-panel-page__section-tab-copy">
                    <span className="control-panel-page__section-tab-label">{definition.label}</span>
                    <span className="control-panel-page__section-tab-title">{definition.title}</span>
                    <span className="control-panel-page__section-tab-summary">{summary}</span>
                  </span>
                </button>
              );
            })}
          </nav>

          <section className="control-panel-page__editor-shell" aria-label="控制面板主编辑区">
            <div className="control-panel-page__section-bar" data-section={activeSection}>
              <div className="control-panel-page__section-bar-main">
                <div className="control-panel-page__section-bar-icon-shell">
                  <SectionIcon className="control-panel-page__section-bar-icon" />
                </div>

                <div className="control-panel-page__section-bar-copy">
                  <Text as="p" size="1" className="control-panel-page__eyebrow">
                    {sectionDefinition.eyebrow}
                  </Text>
                  <Heading size="6" className="control-panel-page__section-bar-title">
                    {sectionDefinition.title}
                  </Heading>
                  <Text as="p" size="2" className="control-panel-page__section-bar-description">
                    {sectionDefinition.description}
                  </Text>
                </div>
              </div>

              <div className="control-panel-page__tag-row">
                {sectionSummary.map((item) => (
                  <span key={item} className="control-panel-page__tag">
                    {item}
                  </span>
                ))}
              </div>
            </div>

            <div className="control-panel-page__editor-scroll">{renderWorkspace()}</div>
          </section>

          <aside className="control-panel-page__action-shell" aria-label="控制面板工具口袋">
            <section className="control-panel-page__sidebar-card control-panel-page__sidebar-card--overview">
              <div className="control-panel-page__sidebar-title-row">
                <ShieldCheck className="control-panel-page__sidebar-icon" />
                <Heading size="4" className="control-panel-page__sidebar-title">
                  运行摘要
                </Heading>
              </div>

              <div className="control-panel-page__metric-list control-panel-page__metric-list--sidebar">
                <BubbleMetric
                  label="pending approvals"
                  value={`${draft.securitySummary.pending_authorizations}`}
                  detail="当前待确认授权数。"
                />
                <BubbleMetric
                  label="today cost"
                  value={`¥${draft.securitySummary.token_cost_summary.today_cost.toFixed(2)}`}
                  detail="今日 token 成本摘要。"
                />
              </div>

              <div className="control-panel-page__note-stack">
                <div className="control-panel-page__note">最新恢复点：{latestRestorePoint}</div>
                <div className="control-panel-page__note">状态：{draft.securitySummary.security_status}</div>
                <div className="control-panel-page__note">
                  单任务上限：{draft.securitySummary.token_cost_summary.single_task_limit.toLocaleString("zh-CN")} tokens
                </div>
                <div className="control-panel-page__note">
                  当日上限：{draft.securitySummary.token_cost_summary.daily_limit.toLocaleString("zh-CN")} tokens
                </div>
              </div>
            </section>

            <section className="control-panel-page__sidebar-card control-panel-page__sidebar-card--actions">
              <div className="control-panel-page__sidebar-title-row">
                <PlayCircle className="control-panel-page__sidebar-icon" />
                <Heading size="4" className="control-panel-page__sidebar-title">
                  操作口袋
                </Heading>
              </div>

              <div className="control-panel-page__tag-row">
                <span className="control-panel-page__tag">当前频率 {inspectionInterval}</span>
                <span className="control-panel-page__tag control-panel-page__tag--quiet">
                  {hasChanges ? "存在待保存修改" : "当前设置已同步"}
                </span>
              </div>

              <Button
                className="control-panel-page__button control-panel-page__button--secondary"
                variant="soft"
                onClick={() => void handleRunInspection()}
                disabled={isRunningInspection}
              >
                <PlayCircle className="h-4 w-4" />
                {isRunningInspection ? "巡检执行中…" : "立即巡检"}
              </Button>

              <div className="control-panel-page__feedback-grid">
                <div className="control-panel-page__feedback-card">
                  <Text as="p" size="1" className="control-panel-page__micro-label">
                    inspection result
                  </Text>
                  <div className="control-panel-page__status-box" aria-live="polite">
                    {inspectionSummary ?? "执行手动巡检后，这里会回写解析结果摘要。"}
                  </div>
                </div>

                <div className="control-panel-page__feedback-card">
                  <Text as="p" size="1" className="control-panel-page__micro-label">
                    save feedback
                  </Text>
                  <div className="control-panel-page__status-box" aria-live="polite">
                    {saveFeedback ?? "修改设置后，这里会展示 apply_mode 与 restart 状态。"}
                  </div>
                </div>
              </div>

              <div className="control-panel-page__action-row">
                <Button
                  className="control-panel-page__button control-panel-page__button--ghost"
                  variant="soft"
                  color="gray"
                  onClick={() => setDraft(panelData)}
                  disabled={!hasChanges || isSaving}
                >
                  撤销修改
                </Button>
                <Button
                  className="control-panel-page__button control-panel-page__button--primary"
                  onClick={() => void handleSave()}
                  disabled={!hasChanges || isSaving}
                >
                  {isSaving ? "保存中…" : "保存设置"}
                </Button>
              </div>
            </section>
          </aside>
        </div>
      </div>
    </main>
  );
}
