import type { ShellBallBubbleMessage as ShellBallBubbleMessageModel } from "../shellBall.bubble";

type ShellBallBubbleMessageProps = {
  message: ShellBallBubbleMessageModel;
};

export function ShellBallBubbleMessage({ message }: ShellBallBubbleMessageProps) {
  return (
    <div
      className={`shell-ball-bubble-zone__message-row shell-ball-bubble-zone__message-row--${message.role}`}
      data-role={message.role}
    >
      <div className={`shell-ball-bubble-message shell-ball-bubble-message--${message.role}`} data-message-id={message.id}>
        <p className="shell-ball-bubble-message__text">{message.text}</p>
      </div>
    </div>
  );
}
