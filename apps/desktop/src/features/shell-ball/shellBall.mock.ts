import type { BubbleMessage, DeliveryResult, Task, TaskStatus } from "@cialloclaw/protocol";

type ShellBallMockResult = {
  task: Task;
  bubble_message: BubbleMessage | null;
  delivery_result: DeliveryResult | null;
};

function createTimestamp() {
  return new Date().toISOString();
}

function createTaskId() {
  return typeof globalThis.crypto?.randomUUID === "function"
    ? `task_mock_${globalThis.crypto.randomUUID()}`
    : `task_mock_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

function buildTask(input: {
  taskId: string;
  title: string;
  sourceType: Task["source_type"];
  status: TaskStatus;
  riskLevel: Task["risk_level"];
  intentName: string;
}) {
  const now = createTimestamp();
  return {
    task_id: input.taskId,
    title: input.title,
    source_type: input.sourceType,
    status: input.status,
    intent: {
      name: input.intentName,
      arguments: {
        mode: "mock",
      },
    },
    current_step: input.status === "confirming_intent" ? "等待确认" : "本地 mock 结果已生成",
    risk_level: input.riskLevel,
    started_at: now,
    updated_at: now,
    finished_at: input.status === "completed" || input.status === "cancelled" ? now : null,
  } satisfies Task;
}

function buildBubble(input: {
  taskId: string;
  text: string;
  type: BubbleMessage["type"];
}) {
  return {
    bubble_id: `bubble_${input.taskId}`,
    task_id: input.taskId,
    type: input.type,
    text: input.text,
    pinned: false,
    hidden: false,
    created_at: createTimestamp(),
  } satisfies BubbleMessage;
}

function buildDeliveryResult(taskId: string, previewText: string): DeliveryResult {
  return {
    type: "bubble",
    title: "Mock Delivery",
    payload: {
      path: null,
      task_id: taskId,
      url: null,
    },
    preview_text: previewText,
  };
}

function normalizeTitle(text: string) {
  const trimmed = text.trim();
  if (trimmed.length <= 18) {
    return trimmed;
  }

  return `${trimmed.slice(0, 18)}...`;
}

function requiresIntentConfirmation(text: string) {
  return /(删除|覆盖|安装|执行|发送|提交|改|写入|移动|重命名|替换)/.test(text);
}

export function createMockShellBallSubmitResult(input: {
  text: string;
  inputMode: "voice" | "text";
}): ShellBallMockResult {
  const normalizedText = input.text.trim();
  const taskId = createTaskId();
  const sourceType = input.inputMode === "voice" ? "voice" : "hover_input";

  if (requiresIntentConfirmation(normalizedText)) {
    const task = buildTask({
      taskId,
      title: normalizeTitle(normalizedText),
      sourceType,
      status: "confirming_intent",
      riskLevel: "yellow",
      intentName: "offline_mock_confirm",
    });
    const bubbleText = "JSON-RPC 当前未连通，已切到 mock 模式。这条请求先模拟成待确认任务，你可以继续点确认或取消。";

    return {
      task,
      bubble_message: buildBubble({
        taskId,
        text: bubbleText,
        type: "intent_confirm",
      }),
      delivery_result: null,
    };
  }

  const previewText = `JSON-RPC 当前未连通，已使用 mock 结果承接：${normalizedText}`;
  const task = buildTask({
    taskId,
    title: normalizeTitle(normalizedText),
    sourceType,
    status: "completed",
    riskLevel: "green",
    intentName: "offline_mock_result",
  });

  return {
    task,
    bubble_message: buildBubble({
      taskId,
      text: previewText,
      type: "result",
    }),
    delivery_result: buildDeliveryResult(taskId, previewText),
  };
}

export function createMockShellBallConfirmResult(input: {
  taskId: string;
  confirmed: boolean;
}): ShellBallMockResult {
  const previewText = input.confirmed
    ? "JSON-RPC 当前未连通，已用 mock 模式继续执行，并生成了一条本地结果。"
    : "JSON-RPC 当前未连通，这条 mock 任务已取消，不会继续执行。";

  const task = buildTask({
    taskId: input.taskId,
    title: input.confirmed ? "Mock Confirmed Task" : "Mock Cancelled Task",
    sourceType: "hover_input",
    status: input.confirmed ? "completed" : "cancelled",
    riskLevel: input.confirmed ? "yellow" : "green",
    intentName: input.confirmed ? "offline_mock_confirmed" : "offline_mock_cancelled",
  });

  return {
    task,
    bubble_message: buildBubble({
      taskId: input.taskId,
      text: previewText,
      type: "result",
    }),
    delivery_result: input.confirmed ? buildDeliveryResult(input.taskId, previewText) : null,
  };
}
