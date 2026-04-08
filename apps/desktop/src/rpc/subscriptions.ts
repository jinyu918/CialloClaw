import { NOTIFICATION_METHODS } from "@cialloclaw/protocol";

// subscribeTask 处理当前模块的相关逻辑。
export function subscribeTask(taskId: string, onMessage: (payload: unknown) => void) {
  const bridge = window.__CIALLOCLAW_NAMED_PIPE__;

  if (!bridge?.subscribe) {
    return () => {};
  }

  let disposed = false;
  let unsubscribe: (() => Promise<void>) | null = null;

  void bridge.subscribe(NOTIFICATION_METHODS.TASK_UPDATED, (payload) => {
    if (disposed) {
      return;
    }

    const message = payload as { params?: { task_id?: string } };
    if (!message.params?.task_id || message.params.task_id === taskId) {
      onMessage(payload);
    }
  }).then((subscription) => {
    if (disposed) {
      void subscription.unsubscribe();
      return;
    }

    unsubscribe = subscription.unsubscribe;
  });

  return () => {
    disposed = true;
    if (unsubscribe) {
      void unsubscribe();
    }
  };
}

export function subscribeMirrorOverviewUpdated(onMessage: (payload: unknown) => void) {
  const bridge = window.__CIALLOCLAW_NAMED_PIPE__;

  if (!bridge?.subscribe) {
    return () => {};
  }

  let disposed = false;
  let unsubscribe: (() => Promise<void>) | null = null;

  void bridge
    .subscribe(NOTIFICATION_METHODS.MIRROR_OVERVIEW_UPDATED, (payload) => {
      if (!disposed) {
        onMessage(payload);
      }
    })
    .then((subscription) => {
      if (disposed) {
        void subscription.unsubscribe();
        return;
      }

      unsubscribe = subscription.unsubscribe;
    });

  return () => {
    disposed = true;
    if (unsubscribe) {
      void unsubscribe();
    }
  };
}
