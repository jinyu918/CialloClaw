import type { PointerEvent, ReactNode, RefObject } from "react";
import type { ShellBallVoicePreview } from "./shellBall.interaction";
import type { ShellBallInputBarMode, ShellBallMotionConfig, ShellBallVisualState } from "./shellBall.types";
import { ShellBallBubbleZone } from "./components/ShellBallBubbleZone";
import { ShellBallInputBar } from "./components/ShellBallInputBar";
import { ShellBallMascot } from "./components/ShellBallMascot";

type ShellBallSurfaceProps = {
  children?: ReactNode;
  contentRef?: RefObject<HTMLDivElement>;
  dragZoneRef?: RefObject<HTMLDivElement>;
  interactionRef?: RefObject<HTMLDivElement>;
  visualState: ShellBallVisualState;
  voicePreview: ShellBallVoicePreview;
  inputBarMode: ShellBallInputBarMode;
  inputValue: string;
  motionConfig: ShellBallMotionConfig;
  onPrimaryClick: () => void;
  onRegionEnter: () => void;
  onRegionLeave: () => void;
  onInputValueChange: (value: string) => void;
  onAttachFile: () => void;
  onSubmitText: () => void;
  onPressStart: (event: PointerEvent<HTMLButtonElement>) => void;
  onPressMove: (event: PointerEvent<HTMLButtonElement>) => void;
  onPressEnd: (event: PointerEvent<HTMLButtonElement>) => boolean;
  onInputFocusChange: (focused: boolean) => void;
  onHostDragStart?: (event: PointerEvent<HTMLDivElement>) => void;
  surfaceRef?: RefObject<HTMLDivElement>;
};

export function ShellBallSurface({
  children,
  visualState,
  voicePreview,
  inputBarMode,
  inputValue,
  motionConfig,
  onPrimaryClick,
  onRegionEnter,
  onRegionLeave,
  onInputValueChange,
  onAttachFile,
  onSubmitText,
  onPressStart,
  onPressMove,
  onPressEnd,
  onInputFocusChange,
  onHostDragStart,
  contentRef,
  dragZoneRef,
  interactionRef,
  surfaceRef,
}: ShellBallSurfaceProps) {
  return (
    <div className="shell-ball-surface" ref={surfaceRef}>
      <div className="shell-ball-surface__core">
        <div className="shell-ball-surface__window-box" ref={contentRef}>
          <div className="shell-ball-surface__stack">
            <ShellBallBubbleZone visualState={visualState} />
            <div className="shell-ball-surface__interaction-shell">
              <div
                className="shell-ball-surface__host-drag-zone"
                data-shell-ball-zone="host-drag"
                aria-hidden="true"
                onPointerDown={onHostDragStart}
                ref={dragZoneRef}
              />
              <div
                className="shell-ball-surface__interaction-zone"
                data-shell-ball-zone="interaction"
                onPointerEnter={onRegionEnter}
                onPointerLeave={onRegionLeave}
                ref={interactionRef}
              >
                <div className="shell-ball-surface__body">
                  <div className="shell-ball-surface__mascot-shell">
                    <ShellBallMascot
                      visualState={visualState}
                      voicePreview={voicePreview}
                      motionConfig={motionConfig}
                      onPrimaryClick={onPrimaryClick}
                      onPressStart={onPressStart}
                      onPressMove={onPressMove}
                      onPressEnd={onPressEnd}
                    />
                  </div>
                  <ShellBallInputBar
                    mode={inputBarMode}
                    voicePreview={voicePreview}
                    value={inputValue}
                    onValueChange={onInputValueChange}
                    onAttachFile={onAttachFile}
                    onSubmit={onSubmitText}
                    onFocusChange={onInputFocusChange}
                  />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      {children}
    </div>
  );
}
