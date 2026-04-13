export const RISK_LEVELS = ["green", "yellow", "red"] as const;

export const SECURITY_STATUSES = [
  "normal",
  "pending_confirmation",
  "intercepted",
  "execution_error",
  "recoverable",
  "recovered",
] as const;

export const TASK_STEP_STATUSES = ["pending", "running", "completed", "failed", "skipped", "cancelled"] as const;
