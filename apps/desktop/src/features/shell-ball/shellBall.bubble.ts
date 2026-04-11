export type ShellBallBubbleMessageRole = "user" | "agent";

export type ShellBallBubbleMessageFreshnessHint = "fresh" | "stale";

export type ShellBallBubbleMessageMotionHint = "pulse" | "settle";

export type ShellBallBubbleMessage = {
  id: string;
  role: ShellBallBubbleMessageRole;
  text: string;
  createdAt: string;
  freshnessHint?: ShellBallBubbleMessageFreshnessHint;
  motionHint?: ShellBallBubbleMessageMotionHint;
};
