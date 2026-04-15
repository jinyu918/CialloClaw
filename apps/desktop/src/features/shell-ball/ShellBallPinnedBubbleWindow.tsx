import { useMemo, useState } from "react";
import {
  getShellBallCurrentWindow,
  getShellBallPinnedBubbleIdFromLabel,
  startShellBallWindowDragging,
} from "../../platform/shellBallWindowController";
import {
  emitShellBallBubbleAction,
  emitShellBallIntentDecision,
  emitShellBallPinnedWindowDetached,
  useShellBallHelperWindowSnapshot,
} from "./useShellBallCoordinator";

export function ShellBallPinnedBubbleWindow() {
  const windowLabel = getShellBallCurrentWindow().label;
  const bubbleId = getShellBallPinnedBubbleIdFromLabel(windowLabel);
  const snapshot = useShellBallHelperWindowSnapshot({ role: "pinned" });
  const [followsShellBallGeometry, setFollowsShellBallGeometry] = useState(true);
  const pinnedItem = useMemo(
    () => snapshot.bubbleItems.find((item) => item.bubble.bubble_id === bubbleId && item.bubble.pinned),
    [bubbleId, snapshot.bubbleItems],
  );

  if (bubbleId === null || pinnedItem === undefined) {
    return <div className="shell-ball-window shell-ball-window--bubble" aria-label="Shell-ball pinned bubble window" />;
  }

  const pinnedBubbleId = bubbleId;

  function handleDetachDrag() {
    if (followsShellBallGeometry) {
      setFollowsShellBallGeometry(false);
      void emitShellBallPinnedWindowDetached(pinnedBubbleId);
    }

    void startShellBallWindowDragging();
  }

  return (
    <div className="shell-ball-window shell-ball-window--bubble" aria-label="Shell-ball pinned bubble window">
      <div className="shell-ball-bubble-message shell-ball-bubble-message--pinned" data-bubble-id={pinnedBubbleId}>
        <button
          type="button"
          className="shell-ball-bubble-message__control shell-ball-bubble-message__pin-control"
          data-bubble-action="unpin"
          data-bubble-id={pinnedBubbleId}
          aria-label="Unpin bubble"
          onClick={() => {
            void emitShellBallBubbleAction("unpin", pinnedBubbleId, "pinned_window");
          }}
        >
          Unpin
        </button>
        <button
          type="button"
          className="shell-ball-bubble-message__control shell-ball-bubble-message__delete-control"
          data-bubble-action="delete"
          data-bubble-id={pinnedBubbleId}
          aria-label="Delete bubble"
          onClick={() => {
            void emitShellBallBubbleAction("delete", pinnedBubbleId, "pinned_window");
          }}
        >
          Delete
        </button>
        <button
          type="button"
          className="shell-ball-bubble-message__drag-handle"
          aria-label="Drag pinned bubble"
          onPointerDown={handleDetachDrag}
        >
          Drag
        </button>
        <p className="shell-ball-bubble-message__text">{pinnedItem.bubble.text}</p>
        {pinnedItem.bubble.type === "intent_confirm" ? (
          <div className="shell-ball-bubble-message__intent-actions">
            <button
              type="button"
              className="shell-ball-bubble-message__intent-button shell-ball-bubble-message__intent-button--muted"
              onClick={() => {
                void emitShellBallIntentDecision("cancel", pinnedItem.bubble.task_id, "pinned_window");
              }}
            >
              取消
            </button>
            <button
              type="button"
              className="shell-ball-bubble-message__intent-button shell-ball-bubble-message__intent-button--primary"
              onClick={() => {
                void emitShellBallIntentDecision("confirm", pinnedItem.bubble.task_id, "pinned_window");
              }}
            >
              确认继续
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
