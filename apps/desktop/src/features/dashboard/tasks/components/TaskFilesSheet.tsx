import { ArrowUpRight, FolderOpenDot, RefreshCcw, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ScrollArea } from "@/components/ui/scroll-area";
import type { Artifact } from "@cialloclaw/protocol";
import type { TaskDetailData } from "../taskPage.types";

type TaskFilesSheetProps = {
  artifactErrorMessage: string | null;
  artifactItems: Artifact[];
  artifactLoading: boolean;
  detailData: TaskDetailData | null;
  onOpenArtifact: (artifactId: string) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenLatestDelivery: () => void;
  onRetryArtifacts: (() => void) | null;
  pendingArtifactId: string | null;
  pendingDeliveryOpen: boolean;
};

export function TaskFilesSheet({
  artifactErrorMessage,
  artifactItems,
  artifactLoading,
  detailData,
  onOpenArtifact,
  open,
  onOpenChange,
  onOpenLatestDelivery,
  onRetryArtifacts,
  pendingArtifactId,
  pendingDeliveryOpen,
}: TaskFilesSheetProps) {
  const files = detailData?.experience.relatedFiles ?? [];

  return (
    <Sheet onOpenChange={onOpenChange} open={open}>
      <SheetContent className="task-capsule-sheet border-0 p-0 text-slate-700" side="right">
        <SheetHeader className="border-b border-white/70 px-6 py-5">
          <SheetTitle className="text-[1.35rem] font-semibold tracking-[-0.03em] text-slate-800">文件舱门</SheetTitle>
          <SheetDescription className="text-sm leading-7 text-slate-600">
            这里集中查看当前任务关联文件、最近产出以及后续可能继续编辑的内容片段。
          </SheetDescription>
        </SheetHeader>

        <ScrollArea className="h-[calc(100%-6.5rem)] px-5 py-5">
          <div className="space-y-6 pb-4">
            <section className="space-y-3">
              <div className="flex items-center gap-2 text-sm font-medium text-slate-800">
                <FolderOpenDot className="h-4 w-4 text-blue-600" />
                关联文件
              </div>
              {files.map((file) => (
                <article key={file.id} className="rounded-[24px] border border-white/70 bg-white/72 p-4 shadow-[0_24px_46px_-36px_rgba(67,85,106,0.42)]">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-slate-800">{file.title}</p>
                      <p className="mt-1 text-xs uppercase tracking-[0.18em] text-slate-500">{file.kind}</p>
                    </div>
                    <span className="rounded-full bg-blue-50 px-3 py-1 text-[0.72rem] text-blue-700">{file.note}</span>
                  </div>
                  <p className="mt-3 rounded-[18px] bg-slate-50/90 px-3 py-2 font-mono text-xs text-slate-500">{file.path}</p>
                </article>
              ))}
            </section>

            <section className="space-y-3">
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2 text-sm font-medium text-slate-800">
                  <Sparkles className="h-4 w-4 text-orange-500" />
                  最近产出
                </div>
                <Button className="h-9 rounded-full px-4 text-sm" disabled={pendingDeliveryOpen} onClick={onOpenLatestDelivery} variant="ghost">
                  <ArrowUpRight className="h-4 w-4" />
                  {pendingDeliveryOpen ? "打开中..." : "打开最新结果"}
                </Button>
              </div>
              {artifactErrorMessage ? (
                <article className="rounded-[24px] border border-rose-100 bg-rose-50/80 p-4 text-sm text-rose-700">
                  <p>{artifactErrorMessage}</p>
                  {onRetryArtifacts ? (
                    <Button className="mt-3 h-9 rounded-full px-4 text-sm" onClick={onRetryArtifacts} variant="ghost">
                      <RefreshCcw className="h-4 w-4" />
                      重试
                    </Button>
                  ) : null}
                </article>
              ) : artifactLoading ? (
                <article className="rounded-[24px] border border-white/70 bg-white/72 p-4 text-sm text-slate-600">正在同步成果列表...</article>
              ) : artifactItems.length > 0 ? (
                artifactItems.map((artifact) => (
                  <article key={artifact.artifact_id} className="rounded-[24px] border border-white/70 bg-white/72 p-4 shadow-[0_24px_46px_-36px_rgba(67,85,106,0.42)]">
                    <div className="flex items-center justify-between gap-3">
                      <div>
                        <p className="font-medium text-slate-800">{artifact.title}</p>
                        <p className="mt-1 text-xs uppercase tracking-[0.18em] text-slate-500">{artifact.artifact_type}</p>
                      </div>
                      <Button
                        className="h-9 rounded-full px-4 text-sm"
                        disabled={pendingArtifactId === artifact.artifact_id}
                        onClick={() => onOpenArtifact(artifact.artifact_id)}
                        variant="ghost"
                      >
                        <ArrowUpRight className="h-4 w-4" />
                        {pendingArtifactId === artifact.artifact_id ? "打开中..." : "打开"}
                      </Button>
                    </div>
                    <p className="mt-3 rounded-[18px] bg-slate-50/90 px-3 py-2 font-mono text-xs text-slate-500">{artifact.path}</p>
                  </article>
                ))
              ) : (
                <article className="rounded-[24px] border border-white/70 bg-white/72 p-4 text-sm text-slate-600">当前还没有可展示的成果。</article>
              )}
            </section>
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}
