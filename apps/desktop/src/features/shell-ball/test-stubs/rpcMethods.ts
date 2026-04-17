import type {
  AgentMirrorOverviewGetResult,
  AgentSecurityAuditListResult,
  AgentSecurityPendingListResult,
  AgentSecurityRespondResult,
  AgentSecurityRestoreApplyResult,
  AgentSecurityRestorePointsListResult,
  AgentSecuritySummaryGetResult,
  AuditRecord,
  DeliveryResult,
  RecoveryPoint,
  TokenCostSummary,
} from "@cialloclaw/protocol";
import { loadSettings } from "@/services/settingsService";

function createDetailedResponse<T>(data: T) {
  return {
    data,
    meta: {
      server_time: new Date().toISOString(),
    },
    warnings: [] as string[],
  };
}

function createTokenCostSummary(): TokenCostSummary {
  return {
    current_task_tokens: 1200,
    current_task_cost: 0.24,
    today_tokens: 9200,
    today_cost: 1.62,
    single_task_limit: 50000,
    daily_limit: 300000,
    budget_auto_downgrade: true,
  };
}

function createRecoveryPoint(): RecoveryPoint {
  return {
    recovery_point_id: "rp_stub",
    task_id: "task_stub",
    summary: "write_file_before_change",
    created_at: new Date().toISOString(),
    objects: ["workspace/stub.txt"],
  };
}

function createAuditRecord(result: AuditRecord["result"] = "success"): AuditRecord {
  return {
    audit_id: `audit_stub_${result}`,
    task_id: "task_stub",
    type: "recovery",
    action: "restore_apply",
    summary: result === "success" ? "已根据恢复点恢复 1 个对象。" : "恢复执行失败。",
    target: "workspace/stub.txt",
    result,
    created_at: new Date().toISOString(),
  };
}

function createSecuritySummary(): AgentSecuritySummaryGetResult {
  return {
    summary: {
      security_status: "normal",
      pending_authorizations: 0,
      latest_restore_point: createRecoveryPoint(),
      token_cost_summary: createTokenCostSummary(),
    },
  };
}

function createSecurityPendingList(): AgentSecurityPendingListResult {
  return {
    items: [],
    page: { limit: 20, offset: 0, total: 0, has_more: false },
  };
}

function createSecurityRespondResult(): AgentSecurityRespondResult {
  return {
    authorization_record: {
      authorization_record_id: "auth_stub",
      task_id: "task_stub",
      approval_id: "approval_stub",
      decision: "allow_once",
      remember_rule: false,
      operator: "test-stub",
      created_at: new Date().toISOString(),
    },
    bubble_message: {
      bubble_id: "bubble_stub",
      task_id: "task_stub",
      type: "status",
      text: "已允许本次操作，任务继续执行。",
      pinned: false,
      hidden: false,
      created_at: new Date().toISOString(),
    },
    impact_scope: {
      apps: [],
      files: [],
      webpages: [],
      out_of_workspace: false,
      overwrite_or_delete_risk: false,
    },
    task: {
      task_id: "task_stub",
      title: "stub task",
      source_type: "hover_input",
      status: "processing",
      intent: null,
      current_step: "security",
      risk_level: "yellow",
      started_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      finished_at: null,
    },
  };
}

function createSecurityRestorePoints(): AgentSecurityRestorePointsListResult {
  return {
    items: [createRecoveryPoint()],
    page: {
      limit: 20,
      offset: 0,
      total: 1,
      has_more: false,
    },
  };
}

function createSecurityAuditList(): AgentSecurityAuditListResult {
  return {
    items: [createAuditRecord()],
    page: {
      limit: 20,
      offset: 0,
      total: 1,
      has_more: false,
    },
  };
}

function createSecurityRestoreApplyResult(): AgentSecurityRestoreApplyResult {
  return {
    applied: false,
    task: {
      task_id: "task_stub",
      title: "stub task",
      source_type: "hover_input",
      status: "waiting_auth",
      intent: null,
      current_step: "restore_point_approval",
      risk_level: "red",
      started_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      finished_at: null,
    },
    recovery_point: createRecoveryPoint(),
    audit_record: null,
    bubble_message: {
      bubble_id: "bubble_restore_stub",
      task_id: "task_stub",
      type: "status",
      text: "恢复操作已进入授权确认。",
      pinned: false,
      hidden: false,
      created_at: new Date().toISOString(),
    },
  };
}

function createMirrorOverview(): AgentMirrorOverviewGetResult {
  return {
    history_summary: ["最近的正式交付以结构化摘要为主。", "风险动作会先进入授权链路。"],
    daily_summary: {
      date: "2026-04-17",
      completed_tasks: 2,
      generated_outputs: 4,
    },
    profile: {
      work_style: "task-centric",
      preferred_output: "3点摘要",
      active_hours: "09:30-19:00",
    },
    memory_references: [
      {
        memory_id: "mem_stub_001",
        reason: "延续既有摘要结构。",
        summary: "优先保留结构化结论与风险提示。",
      },
    ],
  };
}

export async function listSecurityPending(_params?: unknown) {
  return createSecurityPendingList();
}

export async function respondSecurity(_params?: unknown) {
  return createSecurityRespondResult();
}

export async function getSecuritySummary(_params?: unknown) {
  return createSecuritySummary();
}

export async function getSecuritySummaryDetailed(_params?: unknown) {
  return createDetailedResponse(await getSecuritySummary());
}

export async function listSecurityPendingDetailed(_params?: unknown) {
  return createDetailedResponse(await listSecurityPending());
}

export async function respondSecurityDetailed(_params?: unknown) {
  return createDetailedResponse(await respondSecurity());
}

export async function listSecurityRestorePoints(_params?: unknown) {
  return createSecurityRestorePoints();
}

export async function listSecurityRestorePointsDetailed(_params?: unknown) {
  return createDetailedResponse(await listSecurityRestorePoints());
}

export async function applySecurityRestore(_params?: unknown) {
  return createSecurityRestoreApplyResult();
}

export async function applySecurityRestoreDetailed(_params?: unknown) {
  return createDetailedResponse(await applySecurityRestore());
}

export async function listSecurityAudit(_params?: unknown) {
  return createSecurityAuditList();
}

export async function listSecurityAuditDetailed(_params?: unknown) {
  return createDetailedResponse(await listSecurityAudit());
}

export async function getMirrorOverview(_params?: unknown) {
  return createMirrorOverview();
}

export async function getMirrorOverviewDetailed(_params?: unknown) {
  return createDetailedResponse(await getMirrorOverview());
}

export async function submitInput(_params?: unknown) {
  return {
    task: {
      task_id: "task_stub",
      status: "processing",
    },
    bubble_message: null,
  };
}

function createTaskDeliveryResult(): DeliveryResult {
  return {
    type: "task_detail",
    title: "Task detail",
    preview_text: "回到任务详情",
    payload: { path: null, task_id: "task_stub", url: null },
  };
}

export async function startTask(_params?: unknown) {
  return {
    task: {
      task_id: "task_stub",
      status: "processing",
    },
    bubble_message: null,
    delivery_result: null,
  };
}

export async function listTaskArtifacts(_params?: unknown) {
  return {
    items: [],
    page: {
      has_more: false,
      limit: 50,
      offset: 0,
      total: 0,
    },
  };
}

export async function openTaskArtifact(_params?: unknown) {
  return {
    artifact: {
      artifact_id: "artifact_stub",
      artifact_type: "workspace_document",
      mime_type: "text/plain",
      path: "workspace/stub.txt",
      task_id: "task_stub",
      title: "stub.txt",
    },
    delivery_result: createTaskDeliveryResult(),
    open_action: "task_detail" as const,
    resolved_payload: { path: null, task_id: "task_stub", url: null },
  };
}

export async function openDelivery(_params?: unknown) {
  return {
    delivery_result: createTaskDeliveryResult(),
    open_action: "task_detail" as const,
    resolved_payload: { path: null, task_id: "task_stub", url: null },
  };
}

export async function getSettingsDetailed(_params?: unknown) {
  // Dashboard contract tests only need a deterministic snapshot payload.
  return createDetailedResponse(loadSettings());
}

export async function updateSettings(_params?: unknown) {
  // The test stub mirrors the stable settings.update shape without simulating
  // backend-side policy changes, which is enough for mock-mode contract tests.
  const current = loadSettings();

  return {
    apply_mode: "immediate" as const,
    effective_settings: current.settings,
    need_restart: false,
    updated_keys: [],
  };
}
