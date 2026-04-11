import type { ShellBallBubbleItem } from "../shellBall.bubble";

type ShellBallBubbleMessageProps = {
  item: ShellBallBubbleItem;
};

export function ShellBallBubbleMessage({ item }: ShellBallBubbleMessageProps) {
  return (
    <div
      className={`shell-ball-bubble-zone__message-row shell-ball-bubble-zone__message-row--${item.role}`}
      data-role={item.role}
    >
      <div className={`shell-ball-bubble-message shell-ball-bubble-message--${item.role}`} data-message-id={item.bubble.bubble_id}>
        <p className="shell-ball-bubble-message__text">{item.bubble.text}</p>
      </div>
    </div>
  );
}
