import { ShellBallDevLayer } from "./ShellBallDevLayer";
import { shouldShowShellBallDemoSwitcher } from "./shellBall.dev";
import { ShellBallSurface } from "./ShellBallSurface";
import { useShellBallInteraction } from "./useShellBallInteraction";
import { useShellBallWindow } from "./useShellBallWindow";
import { getShellBallMotionConfig } from "./shellBall.motion";
import { isTauriWindowEnvironment } from "@/platform/shellBallWindow";

type ShellBallAppProps = {
  isDev?: boolean;
};

export function ShellBallApp({ isDev = false }: ShellBallAppProps) {
  const {
    visualState,
    inputValue,
    setInputValue,
    voicePreview,
    inputBarMode,
    handlePrimaryClick,
    handleRegionEnter: handleRegionEnterInteraction,
    handleRegionLeave: handleRegionLeaveInteraction,
    handleSubmitText,
    handleAttachFile,
    handlePressStart,
    handlePressMove,
    handlePressEnd,
    handleInputFocusChange,
    handleForceState,
  } = useShellBallInteraction();
  const {
    contentRef,
    dragZoneRef,
    interactionRef,
    surfaceRef,
    handleInteractionEnter: handleWindowInteractionEnter,
    handleInteractionLeave: handleWindowInteractionLeave,
    handleHostDragStart,
  } = useShellBallWindow({
    inputBarMode,
    visualState,
  });
  const motionConfig = getShellBallMotionConfig(visualState);
  const showDemoSwitcher = shouldShowShellBallDemoSwitcher(isDev) && !isTauriWindowEnvironment();

  function handleRegionEnter() {
    handleWindowInteractionEnter();
    handleRegionEnterInteraction();
  }

  function handleRegionLeave() {
    handleRegionLeaveInteraction();
    handleWindowInteractionLeave();
  }

  return (
    <ShellBallSurface
      contentRef={contentRef}
      dragZoneRef={dragZoneRef}
      interactionRef={interactionRef}
      surfaceRef={surfaceRef}
      visualState={visualState}
      voicePreview={voicePreview}
      inputBarMode={inputBarMode}
      inputValue={inputValue}
      motionConfig={motionConfig}
      onPrimaryClick={handlePrimaryClick}
      onRegionEnter={handleRegionEnter}
      onRegionLeave={handleRegionLeave}
      onInputValueChange={setInputValue}
      onAttachFile={handleAttachFile}
      onSubmitText={handleSubmitText}
      onPressStart={handlePressStart}
      onPressMove={handlePressMove}
      onPressEnd={handlePressEnd}
      onInputFocusChange={handleInputFocusChange}
      onHostDragStart={handleHostDragStart}
    >
      {showDemoSwitcher ? (
        <ShellBallDevLayer value={visualState} onChange={handleForceState} />
      ) : null}
    </ShellBallSurface>
  );
}
