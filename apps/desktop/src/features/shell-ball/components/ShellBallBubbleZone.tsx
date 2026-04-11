import type { ShellBallBubbleMessage } from "../shellBall.bubble";
import type { ShellBallVisualState } from "../shellBall.types";
import { ShellBallBubbleMessage as ShellBallBubbleMessageView } from "./ShellBallBubbleMessage";

type ShellBallBubbleZoneProps = {
  visualState: ShellBallVisualState;
  bubbleMessages?: ShellBallBubbleMessage[];
};

export function ShellBallBubbleZone({ visualState, bubbleMessages = [] }: ShellBallBubbleZoneProps) {
  return (
    <section className="shell-ball-bubble-zone" data-state={visualState}>
      <div className="shell-ball-bubble-zone__scroll">
        {bubbleMessages.map((message) => (
          <ShellBallBubbleMessageView key={message.id} message={message} />
        ))}
      </div>
    </section>
  );
}
