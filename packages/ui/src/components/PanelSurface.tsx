import type { PropsWithChildren, ReactNode } from "react";

type PanelSurfaceProps = PropsWithChildren<{
  title: string;
  eyebrow?: string;
  titleAccessory?: ReactNode;
}>;

// PanelSurface renders the shared dashboard card shell.
// The optional titleAccessory lets feature modules place compact controls
// on the same row as the title without rewriting the shared header markup.
export function PanelSurface({ title, eyebrow, titleAccessory, children }: PanelSurfaceProps) {
  return (
    <section className="rounded-3xl border border-white/10 bg-slate-950/55 p-5 shadow-[0_24px_80px_-40px_rgba(14,165,233,0.6)] backdrop-blur">
      <div className="cc-panel-surface__header mb-4">
        {eyebrow ? (
          <p className="cc-panel-surface__eyebrow mb-2 text-xs uppercase tracking-[0.28em] text-cyan-300/80">{eyebrow}</p>
        ) : null}
        <div className="cc-panel-surface__title-row flex flex-wrap items-center justify-between gap-3">
          <h2 className="cc-panel-surface__title text-lg font-semibold text-white">{title}</h2>
          {titleAccessory ? <div className="cc-panel-surface__title-accessory shrink-0">{titleAccessory}</div> : null}
        </div>
      </div>
      {children}
    </section>
  );
}
