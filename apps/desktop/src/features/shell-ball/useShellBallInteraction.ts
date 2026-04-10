import { useEffect, useRef, useState } from "react";
import type { PointerEvent } from "react";
import {
  createShellBallInteractionController,
  getShellBallInputBarMode,
  getShellBallVoicePreview,
  resolveShellBallVoiceReleaseEvent,
  SHELL_BALL_LONG_PRESS_MS,
  shouldRetainShellBallHoverInput,
  type ShellBallVoicePreview,
} from "./shellBall.interaction";
import type { ShellBallInteractionEvent, ShellBallVisualState } from "./shellBall.types";
import { useShellBallStore } from "../../stores/shellBallStore";

type TimeoutHandle = ReturnType<typeof globalThis.setTimeout>;

type ShellBallInteractionController = ReturnType<typeof createShellBallInteractionController>;

export function syncShellBallInteractionController(input: {
  controller: ShellBallInteractionController;
  visualState: ShellBallVisualState;
  regionActive: boolean;
}) {
  if (input.controller.getState() === input.visualState) {
    return input.visualState;
  }

  return input.controller.forceState(input.visualState, { regionActive: input.regionActive });
}

export function useShellBallInteraction() {
  const visualState = useShellBallStore((state) => state.visualState);
  const setVisualState = useShellBallStore((state) => state.setVisualState);
  const [inputValue, setInputValue] = useState("");
  const [voicePreview, setVoicePreview] = useState<ShellBallVoicePreview>(null);
  const regionActiveRef = useRef(false);
  const inputFocusedRef = useRef(false);
  const pressStartXRef = useRef<number | null>(null);
  const pressStartYRef = useRef<number | null>(null);
  const voicePreviewRef = useRef<ShellBallVoicePreview>(null);
  const longPressHandleRef = useRef<TimeoutHandle | null>(null);
  const suppressNextPrimaryClickRef = useRef(false);
  const setVisualStateRef = useRef(setVisualState);
  const controllerRef = useRef<ShellBallInteractionController | null>(null);

  setVisualStateRef.current = setVisualState;

  if (controllerRef.current === null) {
    controllerRef.current = createShellBallInteractionController({
      initialState: visualState,
      schedule: (callback, ms) =>
        globalThis.setTimeout(() => {
          callback();
          setVisualStateRef.current(controllerRef.current?.getState() ?? visualState);
        }, ms),
      cancel: (handle) => {
        globalThis.clearTimeout(handle as TimeoutHandle);
      },
    });
  }

  function syncVisualState() {
    setVisualState(controllerRef.current?.getState() ?? visualState);
  }

  function clearLongPressTimer() {
    if (longPressHandleRef.current === null) {
      return;
    }

    globalThis.clearTimeout(longPressHandleRef.current);
    longPressHandleRef.current = null;
  }

  function setCurrentVoicePreview(preview: ShellBallVoicePreview) {
    voicePreviewRef.current = preview;
    setVoicePreview(preview);
  }

  function getHoverRetained() {
    return shouldRetainShellBallHoverInput({
      regionActive: regionActiveRef.current,
      inputFocused: inputFocusedRef.current,
      hasDraft: inputValue.trim() !== "",
    });
  }

  function dispatch(
    event: ShellBallInteractionEvent,
    options: { regionActive?: boolean; hoverRetained?: boolean } = {},
  ) {
    controllerRef.current?.dispatch(event, {
      regionActive: options.regionActive ?? regionActiveRef.current,
      hoverRetained: options.hoverRetained ?? getHoverRetained(),
    });
    syncVisualState();
  }

  function syncHoverRetention() {
    if (regionActiveRef.current) {
      return;
    }

    if (controllerRef.current?.getState() !== "hover_input") {
      return;
    }

    dispatch("pointer_leave_region", {
      regionActive: false,
      hoverRetained: getHoverRetained(),
    });
  }

  function handlePrimaryClick() {
    if (suppressNextPrimaryClickRef.current) {
      suppressNextPrimaryClickRef.current = false;
      return;
    }

    if (controllerRef.current?.getState() !== "voice_locked") {
      return;
    }

    dispatch("primary_click_locked_voice_end");
  }

  function handleRegionEnter() {
    regionActiveRef.current = true;
    dispatch("pointer_enter_hotspot", { regionActive: true, hoverRetained: false });
  }

  function handleRegionLeave() {
    regionActiveRef.current = false;
    clearLongPressTimer();
    setCurrentVoicePreview(null);
    suppressNextPrimaryClickRef.current = false;
    dispatch("pointer_leave_region", {
      regionActive: false,
      hoverRetained: getHoverRetained(),
    });
  }

  function handleSubmitText() {
    if (inputValue.trim() === "") {
      return;
    }

    dispatch("submit_text");
  }

  function handleAttachFile() {
    dispatch("attach_file");
  }

  function handlePressStart(event: PointerEvent<HTMLButtonElement>) {
    regionActiveRef.current = true;
    pressStartXRef.current = event.clientX;
    pressStartYRef.current = event.clientY;
    setCurrentVoicePreview(null);
    clearLongPressTimer();

    const currentState = controllerRef.current?.getState();
    if (currentState !== "idle" && currentState !== "hover_input") {
      return;
    }

    suppressNextPrimaryClickRef.current = true;

    longPressHandleRef.current = globalThis.setTimeout(() => {
      longPressHandleRef.current = null;
      dispatch("press_start");
    }, SHELL_BALL_LONG_PRESS_MS);
  }

  function handlePressMove(event: PointerEvent<HTMLButtonElement>) {
    if (pressStartXRef.current === null || pressStartYRef.current === null) {
      return;
    }

    const currentState = controllerRef.current?.getState();
    if (currentState !== "voice_listening") {
      return;
    }

    setCurrentVoicePreview(
      getShellBallVoicePreview({
        deltaX: event.clientX - pressStartXRef.current,
        deltaY: event.clientY - pressStartYRef.current,
      }),
    );
  }

  function handlePressEnd(_event: PointerEvent<HTMLButtonElement>) {
    clearLongPressTimer();

    if (controllerRef.current?.getState() === "voice_listening") {
      dispatch(resolveShellBallVoiceReleaseEvent(voicePreviewRef.current));
      pressStartXRef.current = null;
      pressStartYRef.current = null;
      setCurrentVoicePreview(null);
      return true;
    } else if (controllerRef.current?.getState() === "voice_locked") {
      dispatch("primary_click_locked_voice_end");
      pressStartXRef.current = null;
      pressStartYRef.current = null;
      setCurrentVoicePreview(null);
      return true;
    }

    pressStartXRef.current = null;
    pressStartYRef.current = null;
    setCurrentVoicePreview(null);
    return false;
  }

  function handleInputFocusChange(focused: boolean) {
    inputFocusedRef.current = focused;
    if (!focused) {
      syncHoverRetention();
    }
  }

  function handleForceState(state: ShellBallVisualState) {
    clearLongPressTimer();
    pressStartXRef.current = null;
    pressStartYRef.current = null;
    setCurrentVoicePreview(null);
    suppressNextPrimaryClickRef.current = false;
    controllerRef.current?.forceState(state, { regionActive: regionActiveRef.current });
    syncVisualState();
  }

  useEffect(() => {
    syncHoverRetention();
  }, [inputValue]);

  useEffect(() => {
    if (controllerRef.current === null) {
      return;
    }

    syncShellBallInteractionController({
      controller: controllerRef.current,
      visualState,
      regionActive: regionActiveRef.current,
    });
  }, [visualState]);

  useEffect(() => {
    return () => {
      clearLongPressTimer();
      pressStartXRef.current = null;
      pressStartYRef.current = null;
      voicePreviewRef.current = null;
      suppressNextPrimaryClickRef.current = false;
      controllerRef.current?.dispose();
    };
  }, []);

  return {
    visualState,
    inputValue,
    setInputValue,
    voicePreview,
    inputBarMode: getShellBallInputBarMode(visualState),
    handlePrimaryClick,
    handleRegionEnter,
    handleRegionLeave,
    handleSubmitText,
    handleAttachFile,
    handlePressStart,
    handlePressMove,
    handlePressEnd,
    handleInputFocusChange,
    handleForceState,
  };
}
