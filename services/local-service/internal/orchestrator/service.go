// Package orchestrator assembles the owner-4 task-centric backend workflow.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/audit"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/checkpoint"
	contextsvc "github.com/cialloclaw/cialloclaw/services/local-service/internal/context"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/delivery"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/execution"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/intent"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/memory"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/plugin"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/recommendation"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/risk"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/runengine"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/taskinspector"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/tools"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/traceeval"
)

// ErrTaskNotFound indicates that the provided task_id does not exist in the
// current runtime or hydrated query state.
var (
	ErrTaskNotFound           = errors.New("task not found")
	ErrArtifactNotFound       = errors.New("artifact not found")
	ErrTaskStatusInvalid      = errors.New("task status invalid")
	ErrTaskAlreadyFinished    = errors.New("task already finished")
	ErrStorageQueryFailed     = errors.New("storage query failed")
	ErrStrongholdAccessFailed = errors.New("stronghold access failed")
	ErrRecoveryPointNotFound  = errors.New("recovery point not found")
	persistedToolCallEventSeq atomic.Uint64
)

const (
	executionSegmentInitial = "initial"
	executionSegmentResume  = "resume"
	executionSegmentRestart = "restart"
)

// Service is the task-centric orchestration entrypoint for the local-service
// backend.
type Service struct {
	context        *contextsvc.Service
	intent         *intent.Service
	runEngine      *runengine.Engine
	delivery       *delivery.Service
	memory         *memory.Service
	risk           *risk.Service
	model          *model.Service
	tools          *tools.Registry
	plugin         *plugin.Service
	audit          *audit.Service
	recommendation *recommendation.Service
	traceEval      *traceeval.Service
	executor       *execution.Service
	inspector      *taskinspector.Service
	storage        *storage.Service
	runtimeMu      sync.RWMutex
	runtimeNextID  uint64
	runtimeTaps    map[uint64]func(taskID, method string, params map[string]any)
	taskStartTaps  map[uint64]func(taskID, sessionID, traceID string)
}

// budgetDowngradeDecision describes one real execution-time downgrade decision
// so orchestrator can apply lighter execution paths instead of treating the
// setting as a display-only summary field.
type budgetDowngradeDecision struct {
	Enabled        bool
	Applied        bool
	TriggerReason  string
	TriggerStage   string
	DegradeActions []string
	Summary        string
	Trace          map[string]any
}

// NewService wires the main orchestration dependencies.
func NewService(
	context *contextsvc.Service,
	intent *intent.Service,
	runEngine *runengine.Engine,
	delivery *delivery.Service,
	memory *memory.Service,
	risk *risk.Service,
	model *model.Service,
	tools *tools.Registry,
	plugin *plugin.Service,
) *Service {
	return &Service{
		context:        context,
		intent:         intent,
		runEngine:      runEngine,
		delivery:       delivery,
		memory:         memory,
		risk:           risk,
		model:          model,
		tools:          tools,
		plugin:         plugin,
		audit:          audit.NewService(),
		recommendation: recommendation.NewService(),
		traceEval:      traceeval.NewService(nil, nil),
		inspector:      taskinspector.NewService(nil),
		runtimeTaps:    map[uint64]func(taskID, method string, params map[string]any){},
		taskStartTaps:  map[uint64]func(taskID, sessionID, traceID string){},
	}
}

// WithAudit attaches the shared audit service so runtime views do not fork
// their own counters.
func (s *Service) WithAudit(auditService *audit.Service) *Service {
	if auditService != nil {
		s.audit = auditService
	}
	return s
}

// WithExecutor attaches the execution service used by the main task loop.
func (s *Service) WithExecutor(executorService *execution.Service) *Service {
	s.executor = executorService
	if executorService != nil {
		executorService.WithNotificationEmitter(func(taskID, method string, params map[string]any) {
			s.publishRuntimeNotification(taskID, method, params)
			_, _ = s.runEngine.EmitRuntimeNotification(taskID, method, params)
		}).WithSteeringPoller(func(taskID string) []string {
			messages, ok := s.runEngine.DrainSteeringMessages(taskID)
			if !ok {
				return nil
			}
			return messages
		})
	}
	return s
}

// SubscribeRuntimeNotifications registers a temporary tap for execution-time
// runtime notifications so transports can mirror in-flight loop events without
// waiting for the enclosing RPC response to finish.
func (s *Service) SubscribeRuntimeNotifications(listener func(taskID, method string, params map[string]any)) func() {
	if s == nil || listener == nil {
		return func() {}
	}

	s.runtimeMu.Lock()
	s.runtimeNextID++
	listenerID := s.runtimeNextID
	s.runtimeTaps[listenerID] = listener
	s.runtimeMu.Unlock()

	return func() {
		s.runtimeMu.Lock()
		delete(s.runtimeTaps, listenerID)
		s.runtimeMu.Unlock()
	}
}

// SubscribeTaskStarts registers a temporary tap that reports newly created
// tasks before execution continues, allowing transports to associate follow-on
// runtime notifications with requests that did not yet know their task_id.
func (s *Service) SubscribeTaskStarts(listener func(taskID, sessionID, traceID string)) func() {
	if s == nil || listener == nil {
		return func() {}
	}

	s.runtimeMu.Lock()
	s.runtimeNextID++
	listenerID := s.runtimeNextID
	s.taskStartTaps[listenerID] = listener
	s.runtimeMu.Unlock()

	return func() {
		s.runtimeMu.Lock()
		delete(s.taskStartTaps, listenerID)
		s.runtimeMu.Unlock()
	}
}

func (s *Service) publishRuntimeNotification(taskID, method string, params map[string]any) {
	if s == nil {
		return
	}

	s.runtimeMu.RLock()
	if len(s.runtimeTaps) == 0 {
		s.runtimeMu.RUnlock()
		return
	}
	listeners := make([]func(taskID, method string, params map[string]any), 0, len(s.runtimeTaps))
	for _, listener := range s.runtimeTaps {
		listeners = append(listeners, listener)
	}
	s.runtimeMu.RUnlock()

	for _, listener := range listeners {
		listener(taskID, method, cloneMap(params))
	}
}

func (s *Service) publishTaskStart(taskID, sessionID, traceID string) {
	if s == nil {
		return
	}

	s.runtimeMu.RLock()
	if len(s.taskStartTaps) == 0 {
		s.runtimeMu.RUnlock()
		return
	}
	listeners := make([]func(taskID, sessionID, traceID string), 0, len(s.taskStartTaps))
	for _, listener := range s.taskStartTaps {
		listeners = append(listeners, listener)
	}
	s.runtimeMu.RUnlock()

	for _, listener := range listeners {
		listener(taskID, sessionID, traceID)
	}
}

// WithTaskInspector attaches the task-inspector runtime service.
func (s *Service) WithTaskInspector(inspectorService *taskinspector.Service) *Service {
	if inspectorService != nil {
		s.inspector = inspectorService
	}
	return s
}

// WithStorage attaches shared storage for governance and query-side hydration.
func (s *Service) WithStorage(storageService *storage.Service) *Service {
	if storageService != nil {
		s.storage = storageService
	}
	return s
}

// WithTraceEval attaches the owner-5 trace/eval recording service.
func (s *Service) WithTraceEval(traceEvalService *traceeval.Service) *Service {
	if traceEvalService != nil {
		s.traceEval = traceEvalService
	}
	return s
}

// Snapshot returns the minimal orchestrator summary used by debug and health
// endpoints.
func (s *Service) Snapshot() map[string]any {
	pendingApprovals, pendingTotal := s.runEngine.PendingApprovalRequests(100, 0)
	primaryWorker := ""
	if s.plugin != nil {
		if workers := s.plugin.Workers(); len(workers) > 0 {
			primaryWorker = workers[0]
		}
	}
	return map[string]any{
		"context_source":          s.context.Snapshot()["source"],
		"intent_state":            s.intent.Analyze("bootstrap"),
		"task_status":             s.runEngine.CurrentTaskStatus(),
		"run_state":               s.runEngine.CurrentState(),
		"delivery_type":           s.delivery.DefaultResultType(),
		"memory_backend":          s.memory.RetrievalBackend(),
		"risk_level":              s.risk.DefaultLevel(),
		"model":                   s.model.Descriptor(),
		"tool_count":              len(s.tools.Names()),
		"primary_worker":          primaryWorker,
		"pending_approvals":       pendingTotal,
		"latest_approval_request": firstMapOrNil(pendingApprovals),
	}
}

// RunEngine exposes the attached runtime engine for transport-layer tests and
// debug wiring that need to seed notifications or inspect task state.
func (s *Service) RunEngine() *runengine.Engine {
	return s.runEngine
}

func (s *Service) resumeQueuedControlledTask(task runengine.TaskRecord) (runengine.TaskRecord, bool, error) {
	if stringValue(task.Intent, "name", "") != "screen_analyze" {
		return task, false, nil
	}
	approvalRequest, pendingExecution, bubble, err := s.buildScreenAnalysisApprovalState(task)
	if err != nil {
		failedTask, _ := s.failExecutionTask(task, map[string]any{"name": "screen_analyze"}, execution.Result{}, err)
		return failedTask, true, nil
	}
	updatedTask, ok := s.runEngine.MarkWaitingApprovalWithPlan(task.TaskID, approvalRequest, pendingExecution, bubble)
	if !ok {
		return runengine.TaskRecord{}, true, ErrTaskNotFound
	}
	if err := s.persistApprovalRequestState(updatedTask.TaskID, approvalRequest, mapValue(pendingExecution, "impact_scope")); err != nil {
		return runengine.TaskRecord{}, true, err
	}
	return updatedTask, true, nil
}

func clampListLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func clampListOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

// taskMap converts a runengine task record into the protocol-facing task shape.
func taskMap(record runengine.TaskRecord) map[string]any {
	result := map[string]any{
		"task_id":          record.TaskID,
		"session_id":       taskSessionValue(record.SessionID),
		"title":            record.Title,
		"source_type":      record.SourceType,
		"status":           record.Status,
		"intent":           cloneMap(record.Intent),
		"current_step":     record.CurrentStep,
		"risk_level":       record.RiskLevel,
		"loop_stop_reason": record.LoopStopReason,
		"started_at":       record.StartedAt.Format(dateTimeLayout),
		"updated_at":       record.UpdatedAt.Format(dateTimeLayout),
		"finished_at":      nil,
	}
	if record.FinishedAt != nil {
		result["finished_at"] = record.FinishedAt.Format(dateTimeLayout)
	}
	return result
}

func taskSessionValue(sessionID string) any {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return strings.TrimSpace(sessionID)
}

func (s *Service) queueTaskIfSessionBusy(task runengine.TaskRecord) (runengine.TaskRecord, map[string]any, bool, error) {
	activeTask, ok := s.runEngine.ActiveSessionTask(task.SessionID, task.TaskID)
	if !ok {
		return runengine.TaskRecord{}, nil, false, nil
	}

	bubble := s.delivery.BuildBubbleMessage(
		task.TaskID,
		"status",
		fmt.Sprintf("当前会话已有任务 %s 正在执行，本任务已排队等待。", truncateText(activeTask.Title, 24)),
		task.UpdatedAt.Format(dateTimeLayout),
	)
	queuedTask, changed := s.runEngine.QueueTaskForSession(task.TaskID, activeTask.TaskID, bubble)
	if !changed {
		return runengine.TaskRecord{}, nil, false, ErrTaskNotFound
	}
	return queuedTask, bubble, true, nil
}

func (s *Service) drainSessionQueue(sessionID string) error {
	for {
		nextTask, ok := s.runEngine.NextQueuedTaskForSession(sessionID)
		if !ok {
			return nil
		}
		if activeTask, busy := s.runEngine.ActiveSessionTask(sessionID, nextTask.TaskID); busy && activeTask.TaskID != "" {
			return nil
		}

		bubble := s.delivery.BuildBubbleMessage(
			nextTask.TaskID,
			"status",
			"前序任务已完成，当前会话中的下一个任务开始执行。",
			nextTask.UpdatedAt.Format(dateTimeLayout),
		)
		resumedTask, changed := s.runEngine.ResumeQueuedTask(nextTask.TaskID, executionStepName(nextTask.Intent), bubble)
		if !changed {
			return ErrTaskNotFound
		}
		resumedTask, handled, controlledErr := s.resumeQueuedControlledTask(resumedTask)
		if controlledErr != nil {
			return controlledErr
		}
		if handled {
			if taskIsTerminal(resumedTask.Status) {
				continue
			}
			return nil
		}

		governedTask, _, handled, governanceErr := s.handleTaskGovernanceDecision(resumedTask, resumedTask.Intent)
		if governanceErr != nil {
			return governanceErr
		}
		if handled {
			if taskIsTerminal(governedTask.Status) {
				continue
			}
			return nil
		}

		updatedTask, _, _, _, err := s.executeTask(governedTask, snapshotFromTask(governedTask), governedTask.Intent)
		if err != nil {
			return err
		}
		if !taskIsTerminal(updatedTask.Status) {
			return nil
		}
	}
}

func taskIsTerminal(status string) bool {
	switch status {
	case "completed", "cancelled", "ended_unfinished", "failed":
		return true
	default:
		return false
	}
}

// timelineMap converts internal timeline records into protocol-facing values.
func timelineMap(timeline []runengine.TaskStepRecord) []map[string]any {
	result := make([]map[string]any, 0, len(timeline))
	for _, step := range timeline {
		result = append(result, map[string]any{
			"step_id":        step.StepID,
			"task_id":        step.TaskID,
			"name":           step.Name,
			"status":         step.Status,
			"order_index":    step.OrderIndex,
			"input_summary":  step.InputSummary,
			"output_summary": step.OutputSummary,
		})
	}
	return result
}

// pageMap builds the shared paging payload used by list endpoints.
func pageMap(limit, offset, total int) map[string]any {
	return map[string]any{
		"limit":    limit,
		"offset":   offset,
		"total":    total,
		"has_more": offset+limit < total,
	}
}

func (s *Service) listTasksFromStorage(group, sortBy, sortOrder string, limit, offset int) ([]runengine.TaskRecord, int, bool) {
	if s.storage == nil {
		return nil, 0, false
	}
	if s.storage.TaskStore() != nil {
		tasks, total, ok := s.listTasksFromStructuredStorage(group, sortBy, sortOrder, limit, offset)
		if ok {
			return tasks, total, true
		}
	}
	records, err := s.storage.TaskRunStore().LoadLegacyTaskRuns(context.Background(), nil)
	if err != nil || len(records) == 0 {
		return nil, 0, false
	}
	tasks := make([]runengine.TaskRecord, 0, len(records))
	for _, record := range records {
		task := taskRecordFromStorage(record)
		if !matchesTaskGroup(task, group) {
			continue
		}
		tasks = append(tasks, task)
	}
	runengineSortTaskRecords(tasks, sortBy, sortOrder)
	total := len(tasks)
	if offset >= total {
		return []runengine.TaskRecord{}, total, true
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return tasks[offset:end], total, true
}

func (s *Service) listTasksFromStructuredStorage(group, sortBy, sortOrder string, limit, offset int) ([]runengine.TaskRecord, int, bool) {
	records, total, err := s.storage.TaskStore().ListTasks(context.Background(), 0, 0)
	if err != nil || len(records) == 0 {
		return nil, 0, false
	}
	tasks := make([]runengine.TaskRecord, 0, len(records))
	for _, record := range records {
		task, ok := s.structuredTaskRecordToRuntime(record, false)
		if !ok {
			continue
		}
		if !matchesTaskGroup(task, group) {
			continue
		}
		tasks = append(tasks, task)
	}
	if len(tasks) == 0 {
		return nil, 0, false
	}
	runengineSortTaskRecords(tasks, sortBy, sortOrder)
	total = len(tasks)
	if offset >= total {
		return []runengine.TaskRecord{}, total, true
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return tasks[offset:end], total, true
}

func (s *Service) loadAllTasksFromStorage() []runengine.TaskRecord {
	if s.storage == nil {
		return nil
	}
	structuredTasks := []runengine.TaskRecord(nil)
	if s.storage.TaskStore() != nil {
		structuredTasks = s.loadAllTasksFromStructuredStorage()
	}
	if len(structuredTasks) == 0 {
		return s.loadAllTasksFromTaskRunStorage()
	}
	legacyTasks := s.loadLegacyTaskRunsFromStorage(structuredTasks)
	if len(legacyTasks) == 0 {
		return structuredTasks
	}
	return mergeStructuredTaskListCompatibility(structuredTasks, legacyTasks)
}

func (s *Service) loadAllTasksFromTaskRunStorage() []runengine.TaskRecord {
	if s.storage == nil || s.storage.TaskRunStore() == nil {
		return nil
	}
	records, err := s.storage.TaskRunStore().LoadLegacyTaskRuns(context.Background(), nil)
	if err != nil || len(records) == 0 {
		return nil
	}
	tasks := make([]runengine.TaskRecord, 0, len(records))
	for _, record := range records {
		tasks = append(tasks, taskRecordFromStorage(record))
	}
	return tasks
}

func (s *Service) loadLegacyTaskRunsFromStorage(structuredTasks []runengine.TaskRecord) []runengine.TaskRecord {
	if s.storage == nil || s.storage.TaskRunStore() == nil {
		return nil
	}
	structuredTaskIDs := make([]string, 0, len(structuredTasks))
	for _, task := range structuredTasks {
		if strings.TrimSpace(task.TaskID) == "" {
			continue
		}
		structuredTaskIDs = append(structuredTaskIDs, task.TaskID)
	}
	records, err := s.storage.TaskRunStore().LoadLegacyTaskRuns(context.Background(), structuredTaskIDs)
	if err != nil || len(records) == 0 {
		return nil
	}
	tasks := make([]runengine.TaskRecord, 0, len(records))
	for _, record := range records {
		tasks = append(tasks, taskRecordFromStorage(record))
	}
	return tasks
}

// mergeStructuredTaskListCompatibility keeps first-class task rows authoritative
// while still appending legacy task_run-only entries so partially migrated
// databases do not lose pre-structured history in task-centric overview queries.
func mergeStructuredTaskListCompatibility(structuredTasks, taskRunTasks []runengine.TaskRecord) []runengine.TaskRecord {
	if len(structuredTasks) == 0 {
		return taskRunTasks
	}
	if len(taskRunTasks) == 0 {
		return structuredTasks
	}
	merged := make([]runengine.TaskRecord, 0, len(structuredTasks)+len(taskRunTasks))
	seen := make(map[string]struct{}, len(structuredTasks)+len(taskRunTasks))
	for _, task := range structuredTasks {
		merged = append(merged, task)
		seen[task.TaskID] = struct{}{}
	}
	for _, task := range taskRunTasks {
		if _, ok := seen[task.TaskID]; ok {
			continue
		}
		merged = append(merged, task)
	}
	return merged
}

func (s *Service) loadAllTasksFromStructuredStorage() []runengine.TaskRecord {
	records, _, err := s.storage.TaskStore().ListTasks(context.Background(), 0, 0)
	if err != nil || len(records) == 0 {
		return nil
	}
	tasks := make([]runengine.TaskRecord, 0, len(records))
	for _, record := range records {
		task, ok := s.structuredTaskRecordToRuntime(record, false)
		if !ok {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks
}

// taskQueryViews caches runtime and storage-backed task snapshots for one
// request so overview endpoints can reuse one merged task-centric read model
// without reloading the full task table for every widget.
type taskQueryViews struct {
	service      *Service
	runtimeTasks map[string][]runengine.TaskRecord
	mergedTasks  map[string][]runengine.TaskRecord
	storageTasks []runengine.TaskRecord
	storageReady bool
}

func newTaskQueryViews(service *Service) *taskQueryViews {
	return &taskQueryViews{
		service:      service,
		runtimeTasks: make(map[string][]runengine.TaskRecord, 2),
		mergedTasks:  make(map[string][]runengine.TaskRecord, 2),
	}
}

// tasks returns one merged task-centric view for the requested group and sort
// order, reusing the same storage snapshot for the whole RPC request.
func (q *taskQueryViews) tasks(group, sortBy, sortOrder string) []runengine.TaskRecord {
	key := strings.Join([]string{group, sortBy, sortOrder}, "|")
	if tasks, ok := q.mergedTasks[key]; ok {
		return tasks
	}
	runtimeTasks := q.runtime(group, sortBy, sortOrder)
	storageTasks := filterAndSortTasks(q.loadStorage(), group, sortBy, sortOrder)
	merged := mergeTaskLists(runtimeTasks, storageTasks)
	if len(merged) > 0 {
		runengineSortTaskRecords(merged, sortBy, sortOrder)
	}
	q.mergedTasks[key] = merged
	return merged
}

func (q *taskQueryViews) hasRuntimeState() bool {
	return len(q.runtime("unfinished", "updated_at", "desc")) > 0 ||
		len(q.runtime("finished", "finished_at", "desc")) > 0
}

func (q *taskQueryViews) runtime(group, sortBy, sortOrder string) []runengine.TaskRecord {
	key := strings.Join([]string{group, sortBy, sortOrder}, "|")
	if tasks, ok := q.runtimeTasks[key]; ok {
		return tasks
	}
	tasks, _ := q.service.runEngine.ListTasks(group, sortBy, sortOrder, 0, 0)
	q.runtimeTasks[key] = tasks
	return tasks
}

func (q *taskQueryViews) loadStorage() []runengine.TaskRecord {
	if q.storageReady {
		return q.storageTasks
	}
	q.storageTasks = q.service.loadAllTasksFromStorage()
	q.storageReady = true
	return q.storageTasks
}

func filterAndSortTasks(tasks []runengine.TaskRecord, group, sortBy, sortOrder string) []runengine.TaskRecord {
	if len(tasks) == 0 {
		return nil
	}
	filtered := make([]runengine.TaskRecord, 0, len(tasks))
	for _, task := range tasks {
		if matchesTaskGroup(task, group) {
			filtered = append(filtered, task)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	runengineSortTaskRecords(filtered, sortBy, sortOrder)
	return filtered
}

func mergeTaskLists(runtimeTasks, storageTasks []runengine.TaskRecord) []runengine.TaskRecord {
	if len(runtimeTasks) == 0 {
		return storageTasks
	}
	if len(storageTasks) == 0 {
		return runtimeTasks
	}
	runtimeByID := make(map[string]runengine.TaskRecord, len(runtimeTasks))
	for _, task := range runtimeTasks {
		runtimeByID[task.TaskID] = task
	}
	merged := make([]runengine.TaskRecord, 0, len(runtimeTasks)+len(storageTasks))
	seen := make(map[string]struct{}, len(runtimeTasks)+len(storageTasks))
	for _, task := range storageTasks {
		if runtimeTask, ok := runtimeByID[task.TaskID]; ok {
			merged = append(merged, fresherTaskRecord(runtimeTask, task))
			seen[task.TaskID] = struct{}{}
			continue
		}
		merged = append(merged, task)
		seen[task.TaskID] = struct{}{}
	}
	for _, task := range runtimeTasks {
		if _, ok := seen[task.TaskID]; ok {
			continue
		}
		merged = append(merged, task)
	}
	return merged
}

func fresherTaskRecord(runtimeTask, storageTask runengine.TaskRecord) runengine.TaskRecord {
	if runtimeTask.UpdatedAt.After(storageTask.UpdatedAt) {
		return runtimeTask
	}
	if storageTask.UpdatedAt.After(runtimeTask.UpdatedAt) {
		return storageTask
	}
	if runtimeTask.FinishedAt != nil && storageTask.FinishedAt == nil {
		return runtimeTask
	}
	if storageTask.FinishedAt != nil && runtimeTask.FinishedAt == nil {
		return storageTask
	}
	return storageTask
}

func (s *Service) taskDetailFromStorage(taskID string) (runengine.TaskRecord, bool) {
	if s.storage == nil || strings.TrimSpace(taskID) == "" {
		return runengine.TaskRecord{}, false
	}
	if s.storage.TaskStore() != nil {
		if task, record, ok := s.taskDetailFromStructuredStorage(taskID); ok {
			if structuredTaskNeedsTaskRunFallback(record, task) {
				if taskRunTask, taskRunOK := s.taskDetailFromTaskRunStorage(taskID); taskRunOK {
					task = mergeStructuredTaskDetailCompatibility(task, taskRunTask)
				}
			}
			return task, true
		}
	}
	if taskRunTask, ok := s.taskDetailFromTaskRunStorage(taskID); ok {
		return taskRunTask, true
	}
	return runengine.TaskRecord{}, false
}

func (s *Service) taskDetailFromTaskRunStorage(taskID string) (runengine.TaskRecord, bool) {
	if s.storage == nil || s.storage.TaskRunStore() == nil || strings.TrimSpace(taskID) == "" {
		return runengine.TaskRecord{}, false
	}
	record, err := s.storage.TaskRunStore().GetTaskRun(context.Background(), taskID)
	if err != nil {
		return runengine.TaskRecord{}, false
	}
	return taskRecordFromStorage(record), true
}

// structuredTaskNeedsTaskRunFallback keeps task-run reads as a recovery path
// whenever snapshot_json is missing or malformed because several legacy detail
// fields still only exist in compatibility snapshots today.
func structuredTaskNeedsTaskRunFallback(record storage.TaskRecord, _ runengine.TaskRecord) bool {
	if strings.TrimSpace(record.SnapshotJSON) != "" {
		if _, err := storageTaskRunRecordFromSnapshotJSON(record.SnapshotJSON); err == nil {
			return false
		}
	}
	return true
}

// mergeStructuredTaskDetailCompatibility fills task-detail fields that are
// still sourced from task-run snapshots while the first-class task tables are
// being rolled out. The structured row stays authoritative and the task-run
// snapshot only backfills fields the structured read could not rebuild.
func mergeStructuredTaskDetailCompatibility(task, taskRunTask runengine.TaskRecord) runengine.TaskRecord {
	if task.FinishedAt == nil && taskRunTask.FinishedAt != nil {
		task.FinishedAt = cloneTimePointer(taskRunTask.FinishedAt)
	}
	if len(task.Timeline) == 0 {
		task.Timeline = append([]runengine.TaskStepRecord(nil), taskRunTask.Timeline...)
	}
	if isEmptySnapshot(task.Snapshot) {
		task.Snapshot = cloneTaskSnapshot(taskRunTask.Snapshot)
	}
	if len(task.BubbleMessage) == 0 {
		task.BubbleMessage = cloneMap(taskRunTask.BubbleMessage)
	}
	if len(task.DeliveryResult) == 0 {
		task.DeliveryResult = cloneMap(taskRunTask.DeliveryResult)
	}
	if len(task.Artifacts) == 0 {
		task.Artifacts = cloneMapSlice(taskRunTask.Artifacts)
	}
	if len(task.Citations) == 0 {
		task.Citations = cloneMapSlice(taskRunTask.Citations)
	}
	if len(task.AuditRecords) == 0 {
		task.AuditRecords = cloneMapSlice(taskRunTask.AuditRecords)
	}
	if len(task.MirrorReferences) == 0 {
		task.MirrorReferences = cloneMapSlice(taskRunTask.MirrorReferences)
	}
	if len(task.SecuritySummary) == 0 {
		task.SecuritySummary = cloneMap(taskRunTask.SecuritySummary)
	} else {
		for key, value := range taskRunTask.SecuritySummary {
			if _, exists := task.SecuritySummary[key]; !exists {
				task.SecuritySummary[key] = value
			}
		}
	}
	if len(task.ApprovalRequest) == 0 {
		task.ApprovalRequest = cloneMap(taskRunTask.ApprovalRequest)
	}
	if len(task.PendingExecution) == 0 {
		task.PendingExecution = cloneMap(taskRunTask.PendingExecution)
	}
	if len(task.Authorization) == 0 {
		task.Authorization = cloneMap(taskRunTask.Authorization)
	}
	if len(task.ImpactScope) == 0 {
		task.ImpactScope = cloneMap(taskRunTask.ImpactScope)
	}
	if len(task.TokenUsage) == 0 {
		task.TokenUsage = cloneMap(taskRunTask.TokenUsage)
	}
	if len(task.LatestEvent) == 0 {
		task.LatestEvent = cloneMap(taskRunTask.LatestEvent)
	}
	if len(task.LatestToolCall) == 0 {
		task.LatestToolCall = cloneMap(taskRunTask.LatestToolCall)
	}
	if strings.TrimSpace(task.LoopStopReason) == "" {
		task.LoopStopReason = taskRunTask.LoopStopReason
	}
	if len(task.SteeringMessages) == 0 {
		task.SteeringMessages = append([]string(nil), taskRunTask.SteeringMessages...)
	}
	if strings.TrimSpace(task.CurrentStepStatus) == "" {
		task.CurrentStepStatus = taskRunTask.CurrentStepStatus
	}
	return task
}

// latestDeliveryResultFromStorage restores the newest first-class
// delivery_result when structured task detail cannot rely on task_run
// compatibility snapshots anymore.
func (s *Service) latestDeliveryResultFromStorage(taskID string) map[string]any {
	if s == nil || s.storage == nil || s.storage.LoopRuntimeStore() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	record, ok, err := s.storage.LoopRuntimeStore().GetLatestDeliveryResult(context.Background(), taskID)
	if err != nil || !ok {
		return nil
	}
	payload := map[string]any{}
	if strings.TrimSpace(record.PayloadJSON) != "" {
		if err := json.Unmarshal([]byte(record.PayloadJSON), &payload); err != nil {
			payload = map[string]any{}
		}
	}
	return map[string]any{
		"type":         record.Type,
		"title":        record.Title,
		"payload":      payload,
		"preview_text": record.PreviewText,
	}
}

// loadTaskCitationsFromStorage restores the current formal citation chain from
// first-class loop runtime storage when task_run snapshots are unavailable.
func (s *Service) loadTaskCitationsFromStorage(taskID string) []map[string]any {
	if s == nil || s.storage == nil || s.storage.LoopRuntimeStore() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	records, err := s.storage.LoopRuntimeStore().ListTaskCitations(context.Background(), taskID)
	if err != nil {
		return nil
	}
	citations := make([]map[string]any, 0, len(records))
	for _, record := range records {
		citation := map[string]any{
			"citation_id": record.CitationID,
			"task_id":     record.TaskID,
			"run_id":      record.RunID,
			"source_type": record.SourceType,
			"source_ref":  record.SourceRef,
			"label":       record.Label,
		}
		if strings.TrimSpace(record.ArtifactID) != "" {
			citation["artifact_id"] = record.ArtifactID
		}
		if strings.TrimSpace(record.ArtifactType) != "" {
			citation["artifact_type"] = record.ArtifactType
		}
		if strings.TrimSpace(record.EvidenceRole) != "" {
			citation["evidence_role"] = record.EvidenceRole
		}
		if strings.TrimSpace(record.ExcerptText) != "" {
			citation["excerpt_text"] = record.ExcerptText
		}
		if strings.TrimSpace(record.ScreenSessionID) != "" {
			citation["screen_session_id"] = record.ScreenSessionID
		}
		citations = append(citations, citation)
	}
	return citations
}

func (s *Service) taskDetailFromStructuredStorage(taskID string) (runengine.TaskRecord, storage.TaskRecord, bool) {
	record, err := s.storage.TaskStore().GetTask(context.Background(), taskID)
	if err != nil {
		if storage.IsTaskRecordNotFound(err) {
			return runengine.TaskRecord{}, storage.TaskRecord{}, false
		}
		return runengine.TaskRecord{}, storage.TaskRecord{}, false
	}
	task, ok := s.structuredTaskRecordToRuntime(record, true)
	return task, record, ok
}

func matchesTaskGroup(task runengine.TaskRecord, group string) bool {
	switch group {
	case "finished":
		return isFinishedTaskStatus(task.Status)
	default:
		return !isFinishedTaskStatus(task.Status)
	}
}

func isFinishedTaskStatus(status string) bool {
	switch status {
	case "completed", "cancelled", "ended_unfinished", "failed":
		return true
	default:
		return false
	}
}

func runengineSortTaskRecords(tasks []runengine.TaskRecord, sortBy, sortOrder string) {
	switch sortBy {
	case "started_at", "finished_at", "updated_at":
	default:
		sortBy = "updated_at"
	}
	if sortOrder != "asc" {
		sortOrder = "desc"
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		left := taskSortTime(tasks[i], sortBy)
		right := taskSortTime(tasks[j], sortBy)
		if left.Equal(right) {
			leftUpdated := tasks[i].UpdatedAt
			rightUpdated := tasks[j].UpdatedAt
			if leftUpdated.Equal(rightUpdated) {
				if sortOrder == "asc" {
					return tasks[i].TaskID < tasks[j].TaskID
				}
				return tasks[i].TaskID > tasks[j].TaskID
			}
			if sortOrder == "asc" {
				return leftUpdated.Before(rightUpdated)
			}
			return leftUpdated.After(rightUpdated)
		}
		if sortOrder == "asc" {
			return left.Before(right)
		}
		return left.After(right)
	})
}

func countPendingApprovalTasks(tasks []runengine.TaskRecord) int {
	count := 0
	for _, task := range tasks {
		if task.Status == "waiting_auth" && len(task.ApprovalRequest) != 0 {
			count++
		}
	}
	return count
}

func taskSortTime(task runengine.TaskRecord, sortBy string) time.Time {
	switch sortBy {
	case "started_at":
		return task.StartedAt
	case "finished_at":
		if task.FinishedAt != nil {
			return *task.FinishedAt
		}
		return time.Time{}
	default:
		return task.UpdatedAt
	}
}

func taskRecordFromStorage(record storage.TaskRunRecord) runengine.TaskRecord {
	return runengine.TaskRecord{
		TaskID:            record.TaskID,
		SessionID:         record.SessionID,
		RunID:             record.RunID,
		RequestSource:     firstNonEmptyString(strings.TrimSpace(record.RequestSource), strings.TrimSpace(record.Snapshot.Source)),
		RequestTrigger:    firstNonEmptyString(strings.TrimSpace(record.RequestTrigger), strings.TrimSpace(record.Snapshot.Trigger)),
		Title:             record.Title,
		SourceType:        record.SourceType,
		Status:            record.Status,
		Intent:            cloneMap(record.Intent),
		PreferredDelivery: record.PreferredDelivery,
		FallbackDelivery:  record.FallbackDelivery,
		CurrentStep:       record.CurrentStep,
		RiskLevel:         record.RiskLevel,
		StartedAt:         record.StartedAt,
		UpdatedAt:         record.UpdatedAt,
		FinishedAt:        cloneTimePointer(record.FinishedAt),
		Timeline:          timelineFromStorage(record.Timeline),
		BubbleMessage:     cloneMap(record.BubbleMessage),
		DeliveryResult:    cloneMap(record.DeliveryResult),
		Artifacts:         cloneMapSlice(record.Artifacts),
		Citations:         cloneMapSlice(record.Citations),
		AuditRecords:      cloneMapSlice(record.AuditRecords),
		MirrorReferences:  cloneMapSlice(record.MirrorReferences),
		SecuritySummary:   cloneMap(record.SecuritySummary),
		ApprovalRequest:   cloneMap(record.ApprovalRequest),
		PendingExecution:  cloneMap(record.PendingExecution),
		Authorization:     cloneMap(record.Authorization),
		ImpactScope:       cloneMap(record.ImpactScope),
		TokenUsage:        cloneMap(record.TokenUsage),
		MemoryReadPlans:   cloneMapSlice(record.MemoryReadPlans),
		MemoryWritePlans:  cloneMapSlice(record.MemoryWritePlans),
		StorageWritePlan:  cloneMap(record.StorageWritePlan),
		ArtifactPlans:     cloneMapSlice(record.ArtifactPlans),
		LatestEvent:       cloneMap(record.LatestEvent),
		LatestToolCall:    cloneMap(record.LatestToolCall),
		LoopStopReason:    record.LoopStopReason,
		SteeringMessages:  append([]string(nil), record.SteeringMessages...),
		CurrentStepStatus: record.CurrentStepStatus,
	}
}

// structuredTaskRecordToRuntime hydrates one task-centric read model from the
// new first-class tasks/task_steps tables while still reusing snapshot_json as
// the compatibility bridge for fields that are not fully normalized yet.
func (s *Service) structuredTaskRecordToRuntime(record storage.TaskRecord, includeCompatibility bool) (runengine.TaskRecord, bool) {
	var snapshotCompatibility runengine.TaskRecord
	var snapshotCompatibilityOK bool
	if strings.TrimSpace(record.SnapshotJSON) != "" {
		snapshot, err := storageTaskRunRecordFromSnapshotJSON(record.SnapshotJSON)
		if err == nil {
			snapshotCompatibility = taskRecordFromStorage(snapshot)
			snapshotCompatibilityOK = true
		}
	}
	startedAt, err := time.Parse(time.RFC3339Nano, record.StartedAt)
	if err != nil {
		return runengine.TaskRecord{}, false
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil {
		return runengine.TaskRecord{}, false
	}
	var finishedAt *time.Time
	if strings.TrimSpace(record.FinishedAt) != "" {
		parsedFinishedAt, err := time.Parse(time.RFC3339Nano, record.FinishedAt)
		if err == nil {
			finishedAt = &parsedFinishedAt
		}
	}
	intentArguments := map[string]any{}
	if strings.TrimSpace(record.IntentArgumentsJSON) != "" {
		if err := json.Unmarshal([]byte(record.IntentArgumentsJSON), &intentArguments); err != nil {
			intentArguments = map[string]any{}
		}
	}
	runtime := runengine.TaskRecord{
		TaskID:            record.TaskID,
		SessionID:         record.SessionID,
		RunID:             strings.TrimSpace(record.RunID),
		RequestSource:     record.RequestSource,
		RequestTrigger:    record.RequestTrigger,
		Title:             record.Title,
		SourceType:        record.SourceType,
		Status:            record.Status,
		Intent:            map[string]any{"name": record.IntentName, "arguments": intentArguments},
		PreferredDelivery: record.PreferredDelivery,
		FallbackDelivery:  record.FallbackDelivery,
		CurrentStep:       record.CurrentStep,
		RiskLevel:         record.RiskLevel,
		StartedAt:         startedAt,
		UpdatedAt:         updatedAt,
		FinishedAt:        finishedAt,
		Timeline:          s.taskTimelineFromStructuredStorage(record.TaskID),
		CurrentStepStatus: record.CurrentStepStatus,
	}
	s.hydrateStructuredTaskFormalArtifacts(&runtime)
	s.hydrateStructuredTaskSessionAndRun(&runtime)
	s.hydrateStructuredTaskGovernance(&runtime)
	if snapshotCompatibilityOK {
		runtime = mergeStructuredTaskDetailCompatibility(runtime, snapshotCompatibility)
	}
	return runtime, true
}

// hydrateStructuredTaskFormalArtifacts rebuilds task-facing evidence fields from
// first-class stores before any task_run compatibility fallback is considered.
func (s *Service) hydrateStructuredTaskFormalArtifacts(task *runengine.TaskRecord) {
	if s == nil || s.storage == nil || task == nil {
		return
	}
	task.Artifacts = s.loadArtifactsFromStorage(task.TaskID, 0, 0)
	task.Citations = s.loadTaskCitationsFromStorage(task.TaskID)
	task.AuditRecords = s.loadAuditRecordsFromStorage(task.TaskID, 0, 0)
	task.LatestToolCall = s.latestToolCallFromStorage(task.TaskID, task.RunID)
	if deliveryResult := s.latestDeliveryResultFromStorage(task.TaskID); deliveryResult != nil {
		task.DeliveryResult = deliveryResult
	}
}

// hydrateStructuredTaskSessionAndRun uses the first-class sessions/runs stores
// to keep the formal `session -> task -> run` linkage queryable even when the
// legacy task_run snapshot bridge is absent.
func (s *Service) hydrateStructuredTaskSessionAndRun(task *runengine.TaskRecord) {
	if s == nil || s.storage == nil || task == nil {
		return
	}
	if s.storage.SessionStore() != nil && strings.TrimSpace(task.SessionID) != "" {
		if session, err := s.storage.SessionStore().GetSession(context.Background(), task.SessionID); err == nil {
			if strings.TrimSpace(task.Title) == "" {
				task.Title = session.Title
			}
			if strings.TrimSpace(task.SessionID) == "" {
				task.SessionID = session.SessionID
			}
		}
	}
	if s.storage.LoopRuntimeStore() != nil && strings.TrimSpace(task.RunID) != "" {
		if runRecord, err := s.storage.LoopRuntimeStore().GetRun(context.Background(), task.RunID); err == nil {
			if strings.TrimSpace(task.SessionID) == "" {
				task.SessionID = runRecord.SessionID
			}
			if strings.TrimSpace(task.LoopStopReason) == "" {
				task.LoopStopReason = runRecord.StopReason
			}
		}
	}
}

// hydrateStructuredTaskGovernance rebuilds the task-facing governance fields
// from first-class stores when the snapshot bridge is unavailable.
func (s *Service) hydrateStructuredTaskGovernance(task *runengine.TaskRecord) {
	if s == nil || s.storage == nil || task == nil {
		return
	}
	if authorizationRecord := s.latestAuthorizationRecordFromStorage(task.TaskID); authorizationRecord != nil {
		task.Authorization = authorizationRecord
	}
	if deliveryResult := s.latestDeliveryResultFromStorage(task.TaskID); len(deliveryResult) > 0 {
		task.DeliveryResult = deliveryResult
	}
	if citations := s.loadTaskCitationsFromStorage(task.TaskID); len(citations) > 0 {
		task.Citations = citations
	}
	securitySummary := cloneMap(task.SecuritySummary)
	if securitySummary == nil {
		securitySummary = map[string]any{}
	}
	if approvalRequest := s.pendingApprovalRequestFromStorage(task.TaskID, task.RiskLevel); approvalRequest != nil {
		task.ApprovalRequest = approvalRequest
		securitySummary["pending_authorizations"] = 1
		if strings.TrimSpace(stringValue(approvalRequest, "risk_level", "")) != "" {
			securitySummary["security_status"] = "pending_confirmation"
		}
	} else if task.Status == "waiting_auth" {
		securitySummary["pending_authorizations"] = 0
	}
	if latestRestorePoint := s.latestRestorePointFromStorage(task.TaskID); latestRestorePoint != nil {
		securitySummary["latest_restore_point"] = latestRestorePoint
	}
	task.SecuritySummary = securitySummary
}

// selectTaskDetailAuthorizationRecord prefers the newest formal authorization
// record so task detail does not regress to snapshot-era governance anchors once
// first-class authorization storage is available.
func selectTaskDetailAuthorizationRecord(taskID string, runtimeRecord map[string]any, storageRecord map[string]any) map[string]any {
	normalizedRuntime := normalizeTaskDetailAuthorizationRecord(taskID, runtimeRecord)
	normalizedStorage := normalizeTaskDetailAuthorizationRecord(taskID, storageRecord)
	return preferNewerTaskDetailRecord(normalizedRuntime, normalizedStorage, "created_at")
}

// selectTaskDetailAuditRecord keeps screen tasks anchored to the screen-evidence
// audit chain even when newer generic delivery/runtime audits exist later in the
// same task. Non-screen tasks still use the latest normalized audit record.
func selectTaskDetailAuditRecord(task runengine.TaskRecord, runtimeAuditRecords []map[string]any, storageAuditRecords []map[string]any) map[string]any {
	latestOverall := latestNormalizedTaskAuditRecord(task.TaskID, runtimeAuditRecords, storageAuditRecords)
	if !isScreenTaskDetail(task) {
		return latestOverall
	}
	latestScreen := latestScreenTaskAuditRecord(task.TaskID, runtimeAuditRecords, storageAuditRecords)
	if latestScreen == nil {
		return latestOverall
	}
	if shouldPreferLatestTaskAuditOverScreenAudit(latestOverall, latestScreen) {
		return latestOverall
	}
	return latestScreen
}

// shouldPreferLatestTaskAuditOverScreenAudit keeps screen tasks anchored to
// screen evidence by default, but lets newer terminal governance records such as
// failures or restore_apply outcomes override stale screen-capture success logs.
func shouldPreferLatestTaskAuditOverScreenAudit(latestOverall map[string]any, latestScreen map[string]any) bool {
	if len(latestOverall) == 0 {
		return false
	}
	if len(latestScreen) == 0 {
		return true
	}
	if !parseTaskDetailRecordTime(stringValue(latestOverall, "created_at", "")).After(parseTaskDetailRecordTime(stringValue(latestScreen, "created_at", ""))) {
		return false
	}
	if isScreenTaskAuditRecord(latestOverall) {
		return true
	}
	return isTerminalGovernanceAuditRecord(latestOverall)
}

func latestNormalizedTaskAuditRecord(taskID string, auditGroups ...[]map[string]any) map[string]any {
	var latest map[string]any
	for _, group := range auditGroups {
		for _, auditRecord := range group {
			normalized := normalizeTaskDetailAuditRecord(taskID, auditRecord)
			if normalized == nil {
				continue
			}
			latest = preferNewerTaskDetailRecord(latest, normalized, "created_at")
		}
	}
	return latest
}

func latestScreenTaskAuditRecord(taskID string, auditGroups ...[]map[string]any) map[string]any {
	var latest map[string]any
	for _, group := range auditGroups {
		for _, auditRecord := range group {
			normalized := normalizeTaskDetailAuditRecord(taskID, auditRecord)
			if normalized == nil || !isScreenTaskAuditRecord(normalized) {
				continue
			}
			latest = preferNewerTaskDetailRecord(latest, normalized, "created_at")
		}
	}
	return latest
}

func isScreenTaskAuditRecord(auditRecord map[string]any) bool {
	if len(auditRecord) == 0 {
		return false
	}
	if strings.TrimSpace(stringValue(auditRecord, "type", "")) == "screen_capture" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(stringValue(auditRecord, "action", "")), "screen.capture.") {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(stringValue(auditRecord, "target", "")))
	return strings.Contains(target, "screen")
}

func isTerminalGovernanceAuditRecord(auditRecord map[string]any) bool {
	if len(auditRecord) == 0 {
		return false
	}
	result := strings.TrimSpace(stringValue(auditRecord, "result", ""))
	if result != "" && result != "success" {
		return true
	}
	action := strings.TrimSpace(stringValue(auditRecord, "action", ""))
	if strings.HasPrefix(action, "restore_") || strings.HasPrefix(action, "authorization_") {
		return true
	}
	return strings.TrimSpace(stringValue(auditRecord, "type", "")) == "recovery"
}

func isScreenTaskDetail(task runengine.TaskRecord) bool {
	if stringValue(task.Intent, "name", "") == "screen_analyze" || strings.TrimSpace(task.SourceType) == "screen_capture" {
		return true
	}
	if strings.TrimSpace(stringValue(task.PendingExecution, "kind", "")) == "screen_analysis" {
		return true
	}
	for _, artifact := range task.Artifacts {
		if strings.TrimSpace(stringValue(artifact, "artifact_type", "")) == "screen_capture" {
			return true
		}
	}
	for _, citation := range task.Citations {
		if strings.TrimSpace(stringValue(citation, "artifact_type", "")) == "screen_capture" || strings.TrimSpace(stringValue(citation, "screen_session_id", "")) != "" {
			return true
		}
	}
	if strings.TrimSpace(stringValue(task.ApprovalRequest, "operation_name", "")) == "screen_capture" {
		return true
	}
	return false
}

func preferNewerTaskDetailRecord(left map[string]any, right map[string]any, timeKey string) map[string]any {
	if len(left) == 0 {
		return cloneMap(right)
	}
	if len(right) == 0 {
		return cloneMap(left)
	}
	leftTime := parseTaskDetailRecordTime(stringValue(left, timeKey, ""))
	rightTime := parseTaskDetailRecordTime(stringValue(right, timeKey, ""))
	if rightTime.After(leftTime) {
		return cloneMap(right)
	}
	return cloneMap(left)
}

func parseTaskDetailRecordTime(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed
	}
	return time.Time{}
}

func (s *Service) pendingApprovalRequestFromStorage(taskID, fallbackRiskLevel string) map[string]any {
	if s == nil || s.storage == nil || s.storage.ApprovalRequestStore() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	records, _, err := s.storage.ApprovalRequestStore().ListApprovalRequests(context.Background(), taskID, 0, 0)
	if err != nil || len(records) == 0 {
		return nil
	}
	for _, record := range records {
		approvalRequest := normalizeTaskDetailApprovalRequest(taskID, fallbackRiskLevel, approvalRequestRecordToMap(record))
		if approvalRequest != nil {
			return approvalRequest
		}
	}
	return nil
}

func (s *Service) latestAuthorizationRecordFromStorage(taskID string) map[string]any {
	if s == nil || s.storage == nil || s.storage.AuthorizationRecordStore() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	items, _, err := s.storage.AuthorizationRecordStore().ListAuthorizationRecords(context.Background(), taskID, 1, 0)
	if err != nil || len(items) == 0 {
		return nil
	}
	return normalizeTaskDetailAuthorizationRecord(taskID, authorizationRecordRecordToMap(items[0]))
}

func approvalRequestRecordToMap(record storage.ApprovalRequestRecord) map[string]any {
	result := map[string]any{
		"approval_id":    record.ApprovalID,
		"task_id":        record.TaskID,
		"operation_name": record.OperationName,
		"risk_level":     record.RiskLevel,
		"target_object":  record.TargetObject,
		"reason":         record.Reason,
		"status":         record.Status,
		"created_at":     record.CreatedAt,
		"updated_at":     record.UpdatedAt,
	}
	if strings.TrimSpace(record.ImpactScopeJSON) != "" {
		var scope map[string]any
		if err := json.Unmarshal([]byte(record.ImpactScopeJSON), &scope); err == nil && len(scope) > 0 {
			result["impact_scope"] = scope
		}
	}
	return result
}

func authorizationRecordRecordToMap(record storage.AuthorizationRecordRecord) map[string]any {
	return map[string]any{
		"authorization_record_id": record.AuthorizationRecordID,
		"task_id":                 record.TaskID,
		"approval_id":             record.ApprovalID,
		"decision":                record.Decision,
		"remember_rule":           record.RememberRule,
		"operator":                record.Operator,
		"created_at":              record.CreatedAt,
	}
}

func (s *Service) taskTimelineFromStructuredStorage(taskID string) []runengine.TaskStepRecord {
	if s.storage == nil || s.storage.TaskStepStore() == nil {
		return nil
	}
	records, _, err := s.storage.TaskStepStore().ListTaskSteps(context.Background(), taskID, 0, 0)
	if err != nil || len(records) == 0 {
		return nil
	}
	result := make([]runengine.TaskStepRecord, 0, len(records))
	for _, step := range records {
		result = append(result, runengine.TaskStepRecord{
			StepID:        step.StepID,
			TaskID:        step.TaskID,
			Name:          step.Name,
			Status:        step.Status,
			OrderIndex:    step.OrderIndex,
			InputSummary:  step.InputSummary,
			OutputSummary: step.OutputSummary,
		})
	}
	return result
}

func storageTaskRunRecordFromSnapshotJSON(payload string) (storage.TaskRunRecord, error) {
	var record storage.TaskRunRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return storage.TaskRunRecord{}, err
	}
	return record, nil
}

func timelineFromStorage(timeline []storage.TaskStepSnapshot) []runengine.TaskStepRecord {
	if len(timeline) == 0 {
		return nil
	}
	result := make([]runengine.TaskStepRecord, len(timeline))
	for index, step := range timeline {
		result[index] = runengine.TaskStepRecord{
			StepID:        step.StepID,
			TaskID:        step.TaskID,
			Name:          step.Name,
			Status:        step.Status,
			OrderIndex:    step.OrderIndex,
			InputSummary:  step.InputSummary,
			OutputSummary: step.OutputSummary,
		}
	}
	return result
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// initialTimeline creates the first timeline step for a new task and derives
// whether that step starts as pending or running.
func initialTimeline(status, currentStep string) []runengine.TaskStepRecord {
	stepStatus := "running"
	if status == "confirming_intent" || status == "waiting_input" {
		stepStatus = "pending"
	}

	outputSummary := "等待继续处理"
	if status == "waiting_input" {
		outputSummary = "等待用户补充输入"
	}

	return []runengine.TaskStepRecord{
		{
			StepID:        fmt.Sprintf("step_%s", currentStep),
			Name:          currentStep,
			Status:        stepStatus,
			OrderIndex:    1,
			InputSummary:  "已识别到当前任务对象",
			OutputSummary: outputSummary,
		},
	}
}

// workspacePathFromSettings extracts the current workspace path from the
// settings snapshot.
func workspacePathFromSettings(settings map[string]any) string {
	general, ok := settings["general"].(map[string]any)
	if !ok {
		return "workspace"
	}
	download, ok := general["download"].(map[string]any)
	if !ok {
		return "workspace"
	}
	return stringValue(download, "workspace_path", "workspace")
}

// defaultMirrorReference creates the sample memory reference returned by the
// mirror module.
func defaultMirrorReference() map[string]any {
	return map[string]any{
		"memory_id": "pref_001",
		"reason":    "当前任务命中了用户的输出偏好",
		"summary":   "偏好简洁三点式摘要",
	}
}

func focusTaskForOverview(unfinishedTasks, finishedTasks []runengine.TaskRecord) (runengine.TaskRecord, bool) {
	if len(unfinishedTasks) > 0 {
		return unfinishedTasks[0], true
	}
	if len(finishedTasks) > 0 {
		return finishedTasks[0], true
	}
	return runengine.TaskRecord{}, false
}

func nextActionForTask(task runengine.TaskRecord) string {
	switch task.Status {
	case "confirming_intent":
		return "确认当前意图"
	case "waiting_auth":
		return "处理待授权操作"
	case "waiting_input":
		return "补充输入内容"
	case "processing":
		return "等待处理完成"
	case "completed":
		return "查看交付结果"
	default:
		return "打开任务详情"
	}
}

func buildDashboardQuickActions(hasFocusTask bool, pendingTotal, finishedCount int) []string {
	actions := make([]string, 0, 3)
	if pendingTotal > 0 {
		actions = append(actions, "处理待授权操作")
	}
	if hasFocusTask {
		actions = append(actions, "打开任务详情")
	}
	if finishedCount > 0 {
		actions = append(actions, "查看最近结果")
	}
	if len(actions) == 0 {
		actions = append(actions, "等待新任务")
	}
	return actions
}

func shouldIncludeOverviewField(includeAll bool, includeSet map[string]struct{}, field string) bool {
	if includeAll {
		return true
	}
	_, ok := includeSet[field]
	return ok
}

func filterDashboardQuickActionsForFocus(actions []string) []string {
	filtered := make([]string, 0, len(actions))
	for _, action := range actions {
		if action == "查看最近结果" {
			continue
		}
		filtered = append(filtered, action)
	}
	if len(filtered) == 0 {
		return []string{"打开任务详情"}
	}
	return filtered
}

func filterDashboardSignalsForFocus(signals []string) []string {
	if len(signals) <= 2 {
		return signals
	}
	return append([]string(nil), signals[:2]...)
}

func dedupeStringSlice(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func buildDashboardSignals(unfinishedTasks, finishedTasks []runengine.TaskRecord, pendingApprovals []map[string]any) []string {
	signals := make([]string, 0, 3)
	if len(unfinishedTasks) > 0 {
		signals = append(signals, fmt.Sprintf("当前有 %d 个未完成任务处于 runtime 管控中。", len(unfinishedTasks)))
	}
	if len(pendingApprovals) > 0 {
		signals = append(signals, fmt.Sprintf("当前有 %d 个待授权操作等待用户确认。", len(pendingApprovals)))
	}
	if latestRestorePointFromTasks(finishedTasks) != nil {
		signals = append(signals, "最近一次正式交付已经生成可回放的恢复点。")
	}
	if len(signals) == 0 {
		signals = append(signals, "主链路当前暂无活跃任务。")
	}
	return signals
}

func buildDashboardModuleHighlights(unfinishedTasks, finishedTasks []runengine.TaskRecord, pendingTotal int) []string {
	highlights := make([]string, 0, 4)
	if latestOutputPath := latestOutputPathFromTasks(finishedTasks); latestOutputPath != "" {
		highlights = append(highlights, fmt.Sprintf("最近正式交付已落到 %s。", latestOutputPath))
	}
	if pendingTotal > 0 {
		highlights = append(highlights, fmt.Sprintf("当前仍有 %d 个待授权任务等待处理。", pendingTotal))
	}
	if restorePoint := latestRestorePointFromTasks(finishedTasks); restorePoint != nil {
		highlights = append(highlights, fmt.Sprintf("最近恢复点 %s 已可用于安全回显。", stringValue(restorePoint, "recovery_point_id", "latest")))
	}
	if len(unfinishedTasks) > 0 {
		highlights = append(highlights, fmt.Sprintf("最近活跃任务状态为 %s。", unfinishedTasks[0].Status))
	}
	if len(highlights) == 0 {
		highlights = append(highlights, "当前模块视图已切换为 runtime 聚合结果。")
	}
	return highlights
}

func countGeneratedOutputs(tasks []runengine.TaskRecord) int {
	total := 0
	for _, task := range tasks {
		if len(task.DeliveryResult) > 0 || len(task.Artifacts) > 0 {
			total++
		}
	}
	return total
}

func buildDashboardSignalsWithAudit(unfinishedTasks, finishedTasks []runengine.TaskRecord, pendingApprovals []map[string]any, latestAudit map[string]any) []string {
	signals := buildDashboardSignals(unfinishedTasks, finishedTasks, pendingApprovals)
	if latestAudit != nil {
		signals = append(signals, fmt.Sprintf("最近审计摘要：%s。", truncateText(stringValue(latestAudit, "summary", "runtime audit recorded"), 48)))
	}
	return signals
}

func buildDashboardModuleHighlightsWithAudit(unfinishedTasks, finishedTasks []runengine.TaskRecord, pendingTotal int, latestAudit map[string]any) []string {
	highlights := buildDashboardModuleHighlights(unfinishedTasks, finishedTasks, pendingTotal)
	if latestAudit != nil {
		highlights = append(highlights, fmt.Sprintf("最近审计动作：%s -> %s。", truncateText(stringValue(latestAudit, "action", "audit"), 24), truncateText(stringValue(latestAudit, "target", "main_flow"), 36)))
	}
	return highlights
}

func countAuthorizedTasks(taskGroups ...[]runengine.TaskRecord) int {
	total := 0
	for _, tasks := range taskGroups {
		for _, task := range tasks {
			if len(task.Authorization) > 0 {
				total++
			}
		}
	}
	return total
}

func countTasksWithStatus(tasks []runengine.TaskRecord, statuses ...string) int {
	if len(statuses) == 0 {
		return 0
	}
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		if strings.TrimSpace(status) == "" {
			continue
		}
		allowed[status] = struct{}{}
	}
	total := 0
	for _, task := range tasks {
		if _, ok := allowed[task.Status]; ok {
			total++
		}
	}
	return total
}

func countExceptionTasks(taskGroups ...[]runengine.TaskRecord) int {
	total := 0
	for _, tasks := range taskGroups {
		for _, task := range tasks {
			switch task.Status {
			case "failed", "cancelled", "blocked", "ended_unfinished":
				total++
			}
		}
	}
	return total
}

func collectMirrorReferences(tasks []runengine.TaskRecord) []map[string]any {
	references := make([]map[string]any, 0)
	seen := map[string]struct{}{}
	for _, task := range tasks {
		for _, reference := range task.MirrorReferences {
			memoryID := stringValue(reference, "memory_id", "")
			if memoryID == "" {
				continue
			}
			if _, ok := seen[memoryID]; ok {
				continue
			}
			seen[memoryID] = struct{}{}
			references = append(references, cloneMap(reference))
		}
	}
	return references
}

func buildMirrorHistorySummary(tasks []runengine.TaskRecord, memoryReferences []map[string]any) []string {
	if len(tasks) == 0 {
		return []string{"当前还没有完成任务，镜像概览会在首个正式交付后生成。"}
	}

	summaries := []string{
		fmt.Sprintf("最近已完成 %d 个任务，其中 %d 个产出了正式交付。", len(tasks), countGeneratedOutputs(tasks)),
	}
	if len(memoryReferences) > 0 {
		summaries = append(summaries, fmt.Sprintf("当前累计挂接了 %d 条记忆引用，可供 task detail 与 mirror 回显复用。", len(memoryReferences)))
	}
	if latestOutputPath := latestOutputPathFromTasks(tasks); latestOutputPath != "" {
		summaries = append(summaries, fmt.Sprintf("最近一次落盘结果位于 %s。", latestOutputPath))
	}
	return summaries
}

func buildMirrorProfile(tasks []runengine.TaskRecord) map[string]any {
	if len(tasks) == 0 {
		return nil
	}

	documentCount := 0
	bubbleCount := 0
	earliestHour := 24
	latestHour := -1
	for _, task := range tasks {
		switch stringValue(task.DeliveryResult, "type", "") {
		case "workspace_document":
			documentCount++
		case "bubble":
			bubbleCount++
		}
		hour := task.StartedAt.Hour()
		if hour < earliestHour {
			earliestHour = hour
		}
		if hour > latestHour {
			latestHour = hour
		}
	}

	workStyle := "偏好即时结果回显"
	preferredOutput := "bubble"
	if documentCount >= bubbleCount {
		workStyle = "偏好结构化落盘输出"
		preferredOutput = "workspace_document"
	}
	if earliestHour == 24 || latestHour == -1 {
		earliestHour = 0
		latestHour = 0
	}

	return map[string]any{
		"work_style":       workStyle,
		"preferred_output": preferredOutput,
		"active_hours":     fmt.Sprintf("%02d-%02dh", earliestHour, latestHour+1),
	}
}

func aggregateRiskLevel(tasks []runengine.TaskRecord, pendingApprovals []map[string]any, fallback string) string {
	if len(pendingApprovals) > 0 {
		return "red"
	}
	result := fallback
	for _, task := range tasks {
		switch task.RiskLevel {
		case "red":
			return "red"
		case "yellow":
			result = "yellow"
		case "green":
			if result == "" {
				result = "green"
			}
		}
	}
	if result == "" {
		return "green"
	}
	return result
}

func aggregateSecurityStatus(tasks []runengine.TaskRecord, pendingTotal int) string {
	if pendingTotal > 0 {
		return "pending_confirmation"
	}
	for _, task := range tasks {
		status := stringValue(task.SecuritySummary, "security_status", "")
		if status != "" && status != "normal" {
			return status
		}
	}
	return "normal"
}

func latestAuditRecordFromTasks(tasks []runengine.TaskRecord) map[string]any {
	var latestAudit map[string]any
	var latestAt time.Time
	for _, task := range tasks {
		for _, auditRecord := range task.AuditRecords {
			auditAt := parseAuditTime(auditRecord)
			if latestAudit == nil || auditAt.After(latestAt) {
				latestAudit = cloneMap(auditRecord)
				latestAt = auditAt
			}
		}
	}
	return latestAudit
}

func (s *Service) latestAuditRecordFromStorage(taskID string) map[string]any {
	if s.storage == nil {
		return nil
	}
	items, _, err := s.storage.AuditStore().ListAuditRecords(context.Background(), taskID, 1, 0)
	if err != nil || len(items) == 0 {
		return nil
	}
	return normalizeTaskDetailAuditRecord(taskID, items[0].Map())
}

func (s *Service) loadAuditRecordsFromStorage(taskID string, limit, offset int) []map[string]any {
	if s == nil || s.storage == nil || s.storage.AuditStore() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	items, _, err := s.storage.AuditStore().ListAuditRecords(context.Background(), taskID, limit, offset)
	if err != nil {
		return nil
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, item.Map())
	}
	return result
}

func (s *Service) latestToolCallFromStorage(taskID, runID string) map[string]any {
	if s == nil || s.storage == nil || s.storage.ToolCallSink() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	items, _, err := s.storage.ToolCallStore().ListToolCalls(context.Background(), taskID, runID, 1, 0)
	if err != nil || len(items) == 0 {
		return nil
	}
	item := items[0]
	return map[string]any{
		"tool_call_id": item.ToolCallID,
		"run_id":       item.RunID,
		"task_id":      item.TaskID,
		"step_id":      item.StepID,
		"tool_name":    item.ToolName,
		"status":       item.Status,
		"input":        cloneMap(item.Input),
		"output":       cloneMap(item.Output),
		"error_code":   item.ErrorCode,
		"duration_ms":  item.DurationMS,
	}
}

func aggregateTokenCostSummary(unfinishedTasks, finishedTasks []runengine.TaskRecord, budgetAutoDowngrade bool) map[string]any {
	currentTaskTokens := 0
	currentTaskCost := 0.0
	if currentTask, ok := latestTokenUsageTask(unfinishedTasks, finishedTasks); ok {
		currentTaskTokens = intValueFromAny(currentTask.TokenUsage["total_tokens"])
		currentTaskCost = floatValueFromAny(currentTask.TokenUsage["estimated_cost"])
	}

	todayTokens := 0
	todayCost := 0.0
	now := time.Now()
	for _, task := range append(append([]runengine.TaskRecord{}, unfinishedTasks...), finishedTasks...) {
		if !sameDay(task.StartedAt, now) {
			continue
		}
		todayTokens += intValueFromAny(task.TokenUsage["total_tokens"])
		todayCost += floatValueFromAny(task.TokenUsage["estimated_cost"])
	}

	return map[string]any{
		"current_task_tokens":   currentTaskTokens,
		"current_task_cost":     currentTaskCost,
		"today_tokens":          todayTokens,
		"today_cost":            todayCost,
		"single_task_limit":     0.0,
		"daily_limit":           0.0,
		"budget_auto_downgrade": budgetAutoDowngrade,
	}
}

func latestTokenUsageTask(unfinishedTasks, finishedTasks []runengine.TaskRecord) (runengine.TaskRecord, bool) {
	for _, task := range unfinishedTasks {
		if len(task.TokenUsage) > 0 {
			return task, true
		}
	}
	for _, task := range finishedTasks {
		if len(task.TokenUsage) > 0 {
			return task, true
		}
	}
	return runengine.TaskRecord{}, false
}

func parseAuditTime(auditRecord map[string]any) time.Time {
	createdAt := stringValue(auditRecord, "created_at", "")
	if createdAt == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func latestRestorePointFromTasks(tasks []runengine.TaskRecord) map[string]any {
	for _, task := range tasks {
		restorePoint, ok := task.SecuritySummary["latest_restore_point"].(map[string]any)
		if ok && len(restorePoint) > 0 {
			return cloneMap(restorePoint)
		}
	}
	return nil
}

func latestRestorePointFromSummary(summary map[string]any) map[string]any {
	if summary == nil {
		return nil
	}
	latestRestorePoint, ok := summary["latest_restore_point"].(map[string]any)
	if !ok {
		return nil
	}
	return cloneMap(latestRestorePoint)
}

func activeTaskDetailApprovalRequest(task runengine.TaskRecord) map[string]any {
	if task.Status != "waiting_auth" || len(task.ApprovalRequest) == 0 {
		return nil
	}
	return normalizeTaskDetailApprovalRequest(task.TaskID, task.RiskLevel, task.ApprovalRequest)
}

func (s *Service) normalizeTaskDetailRestorePoint(taskID string, securitySummary map[string]any) map[string]any {
	if latestRestorePoint := normalizeTaskDetailRecoveryPoint(taskID, latestRestorePointFromSummary(securitySummary)); latestRestorePoint != nil {
		return latestRestorePoint
	}
	if restorePoint := s.latestRestorePointFromStorage(taskID); restorePoint != nil {
		return restorePoint
	}
	return nil
}

func normalizeTaskDetailApprovalRequest(taskID, fallbackRiskLevel string, approvalRequest map[string]any) map[string]any {
	if len(approvalRequest) == 0 {
		return nil
	}

	approvalID := strings.TrimSpace(stringValue(approvalRequest, "approval_id", ""))
	approvalTaskID := strings.TrimSpace(stringValue(approvalRequest, "task_id", ""))
	operationName := strings.TrimSpace(stringValue(approvalRequest, "operation_name", ""))
	targetObject := strings.TrimSpace(stringValue(approvalRequest, "target_object", ""))
	reason := strings.TrimSpace(stringValue(approvalRequest, "reason", ""))
	status := strings.TrimSpace(stringValue(approvalRequest, "status", ""))
	createdAt := strings.TrimSpace(stringValue(approvalRequest, "created_at", ""))
	riskLevel := strings.TrimSpace(stringValue(approvalRequest, "risk_level", ""))
	if riskLevel == "" {
		riskLevel = strings.TrimSpace(fallbackRiskLevel)
	}

	if approvalID == "" || approvalTaskID != taskID || operationName == "" || targetObject == "" || reason == "" || createdAt == "" {
		return nil
	}
	if status != "pending" || !isSupportedRiskLevel(riskLevel) {
		return nil
	}

	return map[string]any{
		"approval_id":    approvalID,
		"task_id":        approvalTaskID,
		"operation_name": operationName,
		"risk_level":     riskLevel,
		"target_object":  targetObject,
		"reason":         reason,
		"status":         status,
		"created_at":     createdAt,
	}
}

func normalizeTaskDetailRecoveryPoint(taskID string, recoveryPoint map[string]any) map[string]any {
	if len(recoveryPoint) == 0 {
		return nil
	}

	recoveryPointID := strings.TrimSpace(stringValue(recoveryPoint, "recovery_point_id", ""))
	recoveryTaskID := strings.TrimSpace(stringValue(recoveryPoint, "task_id", ""))
	summary := strings.TrimSpace(stringValue(recoveryPoint, "summary", ""))
	createdAt := strings.TrimSpace(stringValue(recoveryPoint, "created_at", ""))
	objects, ok := normalizeStringSlice(recoveryPoint["objects"])
	if !ok {
		return nil
	}

	if recoveryPointID == "" || recoveryTaskID != taskID || summary == "" || createdAt == "" {
		return nil
	}

	return map[string]any{
		"recovery_point_id": recoveryPointID,
		"task_id":           recoveryTaskID,
		"summary":           summary,
		"created_at":        createdAt,
		"objects":           objects,
	}
}

func isSupportedRiskLevel(riskLevel string) bool {
	switch riskLevel {
	case "green", "yellow", "red":
		return true
	default:
		return false
	}
}

func normalizeStringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			items = append(items, text)
		}
		return items, true
	default:
		return nil, false
	}
}

func (s *Service) latestRestorePointFromStorage(taskID string) map[string]any {
	if s.storage == nil {
		return nil
	}
	items, _, err := s.storage.RecoveryPointStore().ListRecoveryPoints(context.Background(), taskID, 1, 0)
	if err != nil || len(items) == 0 {
		return nil
	}
	item := items[0]
	return map[string]any{
		"recovery_point_id": item.RecoveryPointID,
		"task_id":           item.TaskID,
		"summary":           item.Summary,
		"created_at":        item.CreatedAt,
		"objects":           append([]string(nil), item.Objects...),
	}
}

func (s *Service) findRecoveryPointFromStorage(taskID, recoveryPointID string) (checkpoint.RecoveryPoint, error) {
	if s.storage == nil {
		return checkpoint.RecoveryPoint{}, fmt.Errorf("%w: recovery point store unavailable", ErrStorageQueryFailed)
	}
	item, err := s.storage.RecoveryPointStore().GetRecoveryPoint(context.Background(), recoveryPointID)
	if err != nil {
		if errors.Is(err, storage.ErrRecoveryPointNotFound) {
			return checkpoint.RecoveryPoint{}, ErrRecoveryPointNotFound
		}
		return checkpoint.RecoveryPoint{}, fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
	}
	if taskID != "" && item.TaskID != taskID {
		return checkpoint.RecoveryPoint{}, ErrRecoveryPointNotFound
	}
	return item, nil
}

func recoveryPointMap(point checkpoint.RecoveryPoint) map[string]any {
	return map[string]any{
		"recovery_point_id": point.RecoveryPointID,
		"task_id":           point.TaskID,
		"summary":           point.Summary,
		"created_at":        point.CreatedAt,
		"objects":           append([]string(nil), point.Objects...),
	}
}

func restoreApplyAssessment(point checkpoint.RecoveryPoint) execution.GovernanceAssessment {
	impactScope := restoreImpactScope(point)
	return execution.GovernanceAssessment{
		OperationName:      "restore_apply",
		TargetObject:       firstNonEmptyString(firstImpactFile(impactScope), firstNonEmptyString(strings.Join(point.Objects, ", "), "workspace")),
		RiskLevel:          "red",
		ApprovalRequired:   true,
		CheckpointRequired: false,
		Reason:             "policy_requires_authorization",
		ImpactScope:        impactScope,
	}
}

func buildRestoreApplyPendingExecution(point checkpoint.RecoveryPoint, assessment execution.GovernanceAssessment) map[string]any {
	return map[string]any{
		"operation_name":      assessment.OperationName,
		"target_object":       assessment.TargetObject,
		"risk_level":          assessment.RiskLevel,
		"risk_reason":         assessment.Reason,
		"impact_scope":        cloneMap(assessment.ImpactScope),
		"recovery_point_id":   point.RecoveryPointID,
		"checkpoint_required": assessment.CheckpointRequired,
	}
}

func restoreImpactScope(point checkpoint.RecoveryPoint) map[string]any {
	files := append([]string(nil), point.Objects...)
	outOfWorkspace := false
	for _, filePath := range files {
		normalized := strings.TrimSpace(strings.ReplaceAll(filePath, "\\", "/"))
		if normalized == "" {
			continue
		}
		if !strings.HasPrefix(normalized, "workspace/") && normalized != "workspace" {
			outOfWorkspace = true
			break
		}
	}
	return map[string]any{
		"files":                    files,
		"webpages":                 []string{},
		"apps":                     []string{},
		"out_of_workspace":         outOfWorkspace,
		"overwrite_or_delete_risk": true,
	}
}

func firstImpactFile(impactScope map[string]any) string {
	if len(impactScope) == 0 {
		return ""
	}
	files, ok := impactScope["files"].([]string)
	if !ok || len(files) == 0 {
		return ""
	}
	return files[0]
}

func (s *Service) writeRestoreAuditRecord(taskID string, point checkpoint.RecoveryPoint, applied bool, summary string) map[string]any {
	if s.audit == nil {
		return nil
	}
	input := audit.RecordInput{
		TaskID:  taskID,
		Type:    "recovery",
		Action:  "restore_apply",
		Summary: firstNonEmptyString(strings.TrimSpace(summary), "restore apply completed"),
		Target:  firstNonEmptyString(strings.Join(point.Objects, ", "), "recovery_scope"),
		Result:  map[bool]string{true: "success", false: "failed"}[applied],
	}
	if record, err := s.audit.Write(context.Background(), input); err == nil {
		return record.Map()
	}
	if record, err := s.audit.BuildRecord(input); err == nil {
		return record.Map()
	}
	return nil
}

func normalizeTaskDetailAuthorizationRecord(taskID string, authorizationRecord map[string]any) map[string]any {
	if len(authorizationRecord) == 0 {
		return nil
	}

	recordID := strings.TrimSpace(stringValue(authorizationRecord, "authorization_record_id", ""))
	recordTaskID := strings.TrimSpace(stringValue(authorizationRecord, "task_id", ""))
	approvalID := strings.TrimSpace(stringValue(authorizationRecord, "approval_id", ""))
	decision := normalizeTaskDetailAuthorizationDecision(stringValue(authorizationRecord, "decision", ""))
	operator := strings.TrimSpace(stringValue(authorizationRecord, "operator", ""))
	createdAt := strings.TrimSpace(stringValue(authorizationRecord, "created_at", ""))
	if recordID == "" || recordTaskID != taskID || approvalID == "" || decision == "" || operator == "" || createdAt == "" {
		return nil
	}

	return map[string]any{
		"authorization_record_id": recordID,
		"task_id":                 recordTaskID,
		"approval_id":             approvalID,
		"decision":                decision,
		"remember_rule":           boolValue(authorizationRecord, "remember_rule", false),
		"operator":                operator,
		"created_at":              createdAt,
	}
}

func normalizeTaskDetailAuthorizationDecision(decision string) string {
	switch strings.TrimSpace(decision) {
	case "allow_once", "allow_always":
		return "allow_once"
	case "deny_once", "deny_always":
		return "deny_once"
	default:
		return ""
	}
}

func latestFormalTaskAuditRecord(taskID string, auditRecords []map[string]any) map[string]any {
	for index := len(auditRecords) - 1; index >= 0; index-- {
		if normalized := normalizeTaskDetailAuditRecord(taskID, auditRecords[index]); normalized != nil {
			return normalized
		}
	}
	return nil
}

func normalizeTaskDetailAuditRecord(taskID string, auditRecord map[string]any) map[string]any {
	if len(auditRecord) == 0 {
		return nil
	}

	recordID := strings.TrimSpace(firstNonEmptyString(stringValue(auditRecord, "audit_id", ""), stringValue(auditRecord, "audit_record_id", "")))
	recordTaskID := strings.TrimSpace(stringValue(auditRecord, "task_id", ""))
	recordType := strings.TrimSpace(firstNonEmptyString(stringValue(auditRecord, "type", ""), stringValue(auditRecord, "category", "")))
	action := strings.TrimSpace(stringValue(auditRecord, "action", ""))
	summary := strings.TrimSpace(firstNonEmptyString(stringValue(auditRecord, "summary", ""), stringValue(auditRecord, "reason", "")))
	target := strings.TrimSpace(firstNonEmptyString(stringValue(auditRecord, "target", ""), impactScopeTarget(mapValue(auditRecord, "impact_scope"), "")))
	result := strings.TrimSpace(stringValue(auditRecord, "result", ""))
	createdAt := strings.TrimSpace(stringValue(auditRecord, "created_at", ""))
	if recordID == "" || recordTaskID != taskID || recordType == "" || action == "" || summary == "" || target == "" || result == "" || createdAt == "" {
		return nil
	}

	return map[string]any{
		"audit_id":   recordID,
		"task_id":    recordTaskID,
		"type":       recordType,
		"action":     action,
		"summary":    summary,
		"target":     target,
		"result":     result,
		"created_at": createdAt,
	}
}

func taskNotificationsFromStorage(values []storage.NotificationSnapshot) []runengine.NotificationRecord {
	if len(values) == 0 {
		return nil
	}
	result := make([]runengine.NotificationRecord, len(values))
	for index, value := range values {
		result[index] = runengine.NotificationRecord{
			Method:    value.Method,
			Params:    cloneMap(value.Params),
			CreatedAt: value.CreatedAt,
		}
	}
	return result
}

func cloneStorageTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func latestOutputPathFromTasks(tasks []runengine.TaskRecord) string {
	for _, task := range tasks {
		for _, artifact := range task.Artifacts {
			if outputPath := stringValue(artifact, "path", ""); outputPath != "" {
				return outputPath
			}
		}
		if outputPath := pathFromDeliveryResult(task.DeliveryResult); outputPath != "" {
			return outputPath
		}
		if outputPath := stringValue(task.StorageWritePlan, "target_path", ""); outputPath != "" {
			return outputPath
		}
	}
	return ""
}

func (s *Service) refreshMirrorReferences(taskID string) {
	task, ok := s.runEngine.GetTask(taskID)
	if !ok {
		return
	}
	_, _ = s.runEngine.SetMirrorReferences(taskID, buildTaskMirrorReferences(task))
}

func (s *Service) syncTaskReadMirrorReferences(taskID string, references []map[string]any, err error) {
	if err == nil {
		_, _ = s.runEngine.SetMirrorReferences(taskID, cloneMapSlice(references))
		return
	}
	if errors.Is(err, memory.ErrStoreNotConfigured) {
		s.refreshMirrorReferences(taskID)
	}
}

func (s *Service) syncTaskWriteMirrorReferences(taskID string, references []map[string]any, err error) {
	if err == nil {
		_, _ = s.runEngine.SetMirrorReferences(taskID, mergeMirrorReferences(currentTaskMirrorReferences(s.runEngine, taskID), references))
		return
	}
	if errors.Is(err, memory.ErrStoreNotConfigured) {
		s.refreshMirrorReferences(taskID)
	}
}

func buildTaskMirrorReferences(task runengine.TaskRecord) []map[string]any {
	references := make([]map[string]any, 0, len(task.MemoryReadPlans)+len(task.MemoryWritePlans))
	for index, plan := range task.MemoryReadPlans {
		query := firstNonEmptyString(
			stringValue(plan, "query", ""),
			stringValue(plan, "selection_text", ""),
		)
		query = firstNonEmptyString(query, stringValue(plan, "input_text", ""))
		query = firstNonEmptyString(query, task.Title)
		references = append(references, map[string]any{
			"memory_id": fmt.Sprintf("mem_read_%s_%d", task.TaskID, index+1),
			"reason":    firstNonEmptyString(stringValue(plan, "reason", ""), "任务开始前准备记忆召回"),
			"summary":   fmt.Sprintf("召回查询：%s", truncateText(query, 48)),
		})
	}
	for index, plan := range task.MemoryWritePlans {
		summary := firstNonEmptyString(stringValue(plan, "summary", ""), task.Title)
		references = append(references, map[string]any{
			"memory_id": fmt.Sprintf("mem_write_%s_%d", task.TaskID, index+1),
			"reason":    firstNonEmptyString(stringValue(plan, "reason", ""), "任务完成后准备写入记忆摘要"),
			"summary":   truncateText(summary, 64),
		})
	}
	return references
}

func currentTaskMirrorReferences(engine *runengine.Engine, taskID string) []map[string]any {
	if engine == nil {
		return nil
	}
	task, ok := engine.GetTask(taskID)
	if !ok {
		return nil
	}
	return cloneMapSlice(task.MirrorReferences)
}

func mergeMirrorReferences(referenceGroups ...[]map[string]any) []map[string]any {
	merged := make([]map[string]any, 0)
	seen := make(map[string]struct{})
	for _, references := range referenceGroups {
		for _, reference := range references {
			memoryID := stringValue(reference, "memory_id", "")
			if memoryID == "" {
				continue
			}
			if _, ok := seen[memoryID]; ok {
				continue
			}
			seen[memoryID] = struct{}{}
			merged = append(merged, cloneMap(reference))
		}
	}
	return merged
}

func (s *Service) materializeMemoryReadReferences(taskID, runID string, snapshot contextsvc.TaskContextSnapshot) ([]map[string]any, error) {
	if s.memory == nil {
		return nil, memory.ErrStoreNotConfigured
	}
	hits, err := s.memory.Search(context.Background(), memory.RetrievalQuery{
		TaskID: taskID,
		RunID:  runID,
		Query:  memoryQueryFromSnapshot(snapshot),
		Limit:  memory.DefaultSearchLimit,
	})
	if err != nil {
		return nil, err
	}
	persistedHits := cloneRetrievalHitsForTask(taskID, runID, hits)
	if err := s.memory.WriteRetrievalHits(context.Background(), persistedHits); err != nil {
		return nil, err
	}
	return mirrorReferencesFromRetrievalHits(persistedHits), nil
}

func (s *Service) materializeMemoryWriteReferences(taskID, runID string, snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any, deliveryResult map[string]any) ([]map[string]any, error) {
	if s.memory == nil {
		return nil, memory.ErrStoreNotConfigured
	}
	summary := memory.MemorySummary{
		MemorySummaryID: fmt.Sprintf("memsum_%s_%s", taskID, runID),
		TaskID:          taskID,
		RunID:           runID,
		Summary:         buildMemorySummary(snapshot, taskIntent, deliveryResult),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.memory.WriteSummary(context.Background(), summary); err != nil {
		return nil, err
	}
	return []map[string]any{mirrorReferenceFromSummary(summary)}, nil
}

func mirrorReferencesFromRetrievalHits(hits []memory.RetrievalHit) []map[string]any {
	if len(hits) == 0 {
		return nil
	}
	references := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		reason := "当前任务命中了历史记忆"
		if strings.TrimSpace(hit.Source) != "" {
			reason = fmt.Sprintf("当前任务命中了来源为 %s 的历史记忆", hit.Source)
		}
		references = append(references, map[string]any{
			"memory_id": hit.MemoryID,
			"reason":    reason,
			"summary":   truncateText(hit.Summary, 64),
		})
	}
	return references
}

func cloneRetrievalHitsForTask(taskID, runID string, hits []memory.RetrievalHit) []memory.RetrievalHit {
	if len(hits) == 0 {
		return nil
	}
	cloned := make([]memory.RetrievalHit, 0, len(hits))
	for _, hit := range hits {
		hit.TaskID = taskID
		hit.RunID = runID
		hit.RetrievalHitID = ""
		cloned = append(cloned, hit)
	}
	return cloned
}

func mirrorReferenceFromSummary(summary memory.MemorySummary) map[string]any {
	return map[string]any{
		"memory_id": summary.MemorySummaryID,
		"reason":    "任务完成后写入真实记忆摘要",
		"summary":   truncateText(summary.Summary, 64),
	}
}

func deriveImpactScopeFiles(task runengine.TaskRecord, pendingExecution map[string]any, deliveryService *delivery.Service) []string {
	files := make([]string, 0, 4)
	files = appendImpactScopePath(files, stringValue(task.StorageWritePlan, "target_path", ""))
	for _, artifactPlan := range task.ArtifactPlans {
		files = appendImpactScopePath(files, stringValue(artifactPlan, "path", ""))
	}
	files = appendImpactScopePath(files, pathFromDeliveryResult(task.DeliveryResult))
	files = appendImpactScopePath(files, pathFromPendingExecution(task.TaskID, pendingExecution, deliveryService))
	files = appendImpactScopePath(files, targetPathFromIntent(task.Intent))
	return files
}

func appendImpactScopePath(files []string, candidate string) []string {
	candidate = strings.TrimSpace(strings.ReplaceAll(candidate, "\\", "/"))
	if candidate == "" {
		return files
	}
	candidate = path.Clean(candidate)
	if candidate == "." {
		return files
	}
	for _, existing := range files {
		if existing == candidate {
			return files
		}
	}
	return append(files, candidate)
}

func pathFromPendingExecution(taskID string, pendingExecution map[string]any, deliveryService *delivery.Service) string {
	if len(pendingExecution) == 0 {
		return ""
	}
	deliveryType := stringValue(pendingExecution, "delivery_type", "")
	if deliveryType != "workspace_document" {
		return ""
	}
	resultTitle := stringValue(pendingExecution, "result_title", "处理结果")
	previewText := stringValue(pendingExecution, "preview_text", "")
	deliveryResult := deliveryService.BuildDeliveryResult(taskID, deliveryType, resultTitle, previewText)
	return pathFromDeliveryResult(deliveryResult)
}

func pathFromDeliveryResult(deliveryResult map[string]any) string {
	payload, ok := deliveryResult["payload"].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(payload, "path", "")
}

func targetPathFromIntent(taskIntent map[string]any) string {
	targetPath := stringValue(mapValue(taskIntent, "arguments"), "target_path", "")
	switch targetPath {
	case "", "workspace_document", "bubble", "result_page", "task_detail", "open_file", "reveal_in_folder":
		return ""
	default:
		return targetPath
	}
}

func isWorkspaceRelativePath(filePath, workspaceRoot string) bool {
	normalizedRoot := strings.Trim(strings.ReplaceAll(workspaceRoot, "\\", "/"), "/")
	normalizedPath := strings.Trim(strings.ReplaceAll(filePath, "\\", "/"), "/")
	if normalizedRoot == "" {
		normalizedRoot = "workspace"
	}
	return normalizedPath == normalizedRoot || strings.HasPrefix(normalizedPath, normalizedRoot+"/")
}

func hasOverwriteOrDeleteRisk(taskIntent map[string]any) bool {
	if stringValue(taskIntent, "name", "") == "write_file" {
		return true
	}
	arguments := mapValue(taskIntent, "arguments")
	return boolValue(arguments, "overwrite", false) || boolValue(arguments, "delete", false)
}

// attachMemoryReadPlans registers the retrieval plans attached at task start or
// confirmation time. Read plans are persisted before execution so later mirror,
// debug, or storage-backed views can explain what memory lookup the task was
// supposed to perform even if execution changes or the process restarts.
func (s *Service) attachMemoryReadPlans(taskID, runID string, snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any) {
	readPlans := []map[string]any{
		{
			"kind":           "retrieval",
			"backend":        s.memory.RetrievalBackend(),
			"task_id":        taskID,
			"run_id":         runID,
			"query":          memoryQueryFromSnapshot(snapshot),
			"reason":         "任务开始前准备记忆召回",
			"intent_name":    stringValue(taskIntent, "name", "summarize"),
			"selection_text": snapshot.SelectionText,
			"input_text":     snapshot.Text,
			"source_type":    snapshot.Trigger,
		},
	}

	_, _ = s.runEngine.SetMemoryPlans(taskID, readPlans, nil)
	references, err := s.materializeMemoryReadReferences(taskID, runID, snapshot)
	s.syncTaskReadMirrorReferences(taskID, references, err)
}

// attachPostDeliveryHandoffs registers memory-write and delivery persistence
// handoffs after a task finishes. Keeping these side effects in one post-
// delivery step prevents runtime execution from mixing formal delivery with
// memory persistence details while still leaving a durable handoff trail.
func (s *Service) attachPostDeliveryHandoffs(taskID, runID string, snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any, deliveryResult map[string]any, artifacts []map[string]any) {
	writePlans := []map[string]any{
		{
			"kind":        "summary_write",
			"backend":     s.memory.RetrievalBackend(),
			"task_id":     taskID,
			"run_id":      runID,
			"summary":     buildMemorySummary(snapshot, taskIntent, deliveryResult),
			"reason":      "任务完成后准备写入阶段摘要",
			"source_type": snapshot.Trigger,
		},
	}
	_, _ = s.runEngine.SetMemoryPlans(taskID, nil, writePlans)
	references, err := s.materializeMemoryWriteReferences(taskID, runID, snapshot, taskIntent, deliveryResult)
	s.syncTaskWriteMirrorReferences(taskID, references, err)

	storageWritePlan := s.delivery.BuildStorageWritePlan(taskID, deliveryResult)
	artifacts = delivery.EnsureArtifactIdentifiers(taskID, attachDeliveryResultToArtifacts(deliveryResult, artifacts))
	artifactPlans := s.delivery.BuildArtifactPersistPlans(taskID, artifacts)
	_, _ = s.runEngine.SetDeliveryPlans(taskID, storageWritePlan, artifactPlans)
	s.persistArtifacts(taskID, artifactPlans)
}

// buildApprovalRequest creates the normalized approval_request payload. The
// object must already be protocol-facing here because it is persisted, replayed
// to transports, and later echoed back through agent.security.respond.
func buildApprovalRequest(taskID string, taskIntent map[string]any, assessment execution.GovernanceAssessment) map[string]any {
	arguments := mapValue(taskIntent, "arguments")
	targetObject := firstNonEmptyString(assessment.TargetObject, stringValue(arguments, "target_path", "workspace_document"))
	if targetObject == "" {
		targetObject = "workspace_document"
	}

	return map[string]any{
		"approval_id":    fmt.Sprintf("appr_%s_%d", taskID, time.Now().UnixNano()),
		"task_id":        taskID,
		"operation_name": firstNonEmptyString(assessment.OperationName, firstNonEmptyString(stringValue(taskIntent, "name", ""), "write_file")),
		"risk_level":     firstNonEmptyString(assessment.RiskLevel, "red"),
		"target_object":  targetObject,
		"reason":         firstNonEmptyString(assessment.Reason, "policy_requires_authorization"),
		"status":         "pending",
		"created_at":     time.Now().Format(dateTimeLayout),
	}
}

// buildImpactScope derives the minimal impact summary used by authorization
// results and the security views. It intentionally normalizes files around the
// workspace root so policy, audit, and restore flows all reason about one scope
// shape instead of transport- or tool-specific paths.
func (s *Service) buildImpactScope(task runengine.TaskRecord, pendingExecution map[string]any) map[string]any {
	if impactScope, ok := pendingExecution["impact_scope"].(map[string]any); ok && len(impactScope) > 0 {
		return cloneMap(impactScope)
	}
	files := deriveImpactScopeFiles(task, pendingExecution, s.delivery)
	workspacePath := workspacePathFromSettings(s.runEngine.Settings())
	outOfWorkspace := false
	for _, filePath := range files {
		if !isWorkspaceRelativePath(filePath, workspacePath) {
			outOfWorkspace = true
			break
		}
	}

	return map[string]any{
		"files":                    files,
		"webpages":                 []string{},
		"apps":                     []string{},
		"out_of_workspace":         outOfWorkspace,
		"overwrite_or_delete_risk": hasOverwriteOrDeleteRisk(task.Intent),
	}
}

// memoryQueryFromSnapshot selects the most representative retrieval query from
// the current context snapshot. The fallback order intentionally prefers direct
// user focus, then file context, then broader perception signals so memory
// lookup stays anchored to what most likely triggered the task.
func memoryQueryFromSnapshot(snapshot contextsvc.TaskContextSnapshot) string {
	for _, value := range []string{snapshot.SelectionText, snapshot.Text, snapshot.ErrorText} {
		if value != "" {
			return truncateText(value, 64)
		}
	}

	if len(snapshot.Files) > 0 {
		return snapshot.Files[0]
	}

	for _, value := range []string{snapshot.VisibleText, snapshot.ScreenSummary, snapshot.PageTitle, snapshot.WindowTitle, snapshot.ClipboardText} {
		if value != "" {
			return truncateText(value, 64)
		}
	}

	return "task_context"
}

// buildMemorySummary creates the short post-task memory summary written after
// delivery completes. It keeps the output compact on purpose because this text
// is later used as durable memory material rather than a full-fidelity trace.
func buildMemorySummary(snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any, deliveryResult map[string]any) string {
	intentName := stringValue(taskIntent, "name", "summarize")
	title := stringValue(deliveryResult, "title", "任务结果")
	query := memoryQueryFromSnapshot(snapshot)
	preview := stringValue(deliveryResult, "preview_text", "")
	if preview == "" {
		preview = title
	}
	perceptionSummary := []string{}
	if snapshot.CopyCount > 0 || strings.EqualFold(snapshot.LastAction, "copy") {
		perceptionSummary = append(perceptionSummary, "copy")
	}
	if snapshot.DwellMillis > 0 {
		perceptionSummary = append(perceptionSummary, fmt.Sprintf("dwell=%dms", snapshot.DwellMillis))
	}
	if snapshot.WindowSwitches > 0 || snapshot.PageSwitches > 0 {
		perceptionSummary = append(perceptionSummary, fmt.Sprintf("switch=%d/%d", snapshot.WindowSwitches, snapshot.PageSwitches))
	}
	if snapshot.PageTitle != "" {
		perceptionSummary = append(perceptionSummary, "page="+truncateText(snapshot.PageTitle, 24))
	}
	if len(perceptionSummary) == 0 {
		return fmt.Sprintf("任务完成，意图=%s，输入=%s，交付=%s，结果摘要=%s", intentName, truncateText(query, 48), title, truncateText(preview, 96))
	}
	return fmt.Sprintf("任务完成，意图=%s，输入=%s，感知=%s，交付=%s，结果摘要=%s", intentName, truncateText(query, 48), strings.Join(perceptionSummary, ", "), title, truncateText(preview, 96))
}

// resultSpecFromIntent returns the default result title, preview text, and
// completion bubble text for an intent.
func resultSpecFromIntent(taskIntent map[string]any) (string, string, string) {
	switch stringValue(taskIntent, "name", "summarize") {
	case "agent_loop":
		return "处理结果", "结果已通过气泡返回", "结果已经生成，可直接查看。"
	case "rewrite":
		return "改写结果", "已为你写入文档并打开", "内容已经按要求改写完成，可直接查看。"
	case "translate":
		return "翻译结果", "结果已通过气泡返回", "翻译结果已经生成，可直接查看。"
	case "explain":
		return "解释结果", "结果已通过气泡返回", "这段内容的意思已经整理好了。"
	case "page_read":
		return "网页读取结果", "结果已通过气泡返回", "网页主要内容已经整理完成，可直接查看。"
	case "page_search":
		return "网页搜索结果", "结果已通过气泡返回", "网页搜索结果已经返回，可直接查看。"
	case "write_file":
		return "文件写入结果", "已为你写入文档并打开", "文件已经生成，可直接查看。"
	default:
		return "处理结果", "已为你写入文档并打开", "结果已经生成，可直接查看。"
	}
}

// deliveryTypeFromIntent returns the default delivery type for an intent.
func deliveryTypeFromIntent(taskIntent map[string]any) string {
	switch stringValue(taskIntent, "name", "summarize") {
	case "agent_loop", "translate", "explain", "page_read", "page_search":
		return "bubble"
	default:
		return "workspace_document"
	}
}

// buildPendingExecution creates the minimum delivery plan required to resume a
// task after authorization. The stored plan must be deterministic and task-
// centric because waiting_auth can outlive the original request and later needs
// to restart execution without recomputing delivery intent from transport-only
// inputs.
func (s *Service) buildPendingExecution(task runengine.TaskRecord, taskIntent map[string]any) map[string]any {
	plan := s.delivery.BuildApprovalExecutionPlan(task.TaskID, taskIntent)
	return s.applyResolvedDeliveryToPlan(task, plan, taskIntent)
}

func (s *Service) applyGovernanceAssessment(plan map[string]any, assessment execution.GovernanceAssessment) map[string]any {
	updatedPlan := cloneMap(plan)
	if updatedPlan == nil {
		updatedPlan = map[string]any{}
	}
	if len(assessment.ImpactScope) > 0 {
		updatedPlan["impact_scope"] = cloneMap(assessment.ImpactScope)
	}
	if assessment.OperationName != "" {
		updatedPlan["operation_name"] = assessment.OperationName
	}
	if assessment.TargetObject != "" {
		updatedPlan["target_object"] = assessment.TargetObject
	}
	if assessment.RiskLevel != "" {
		updatedPlan["risk_level"] = assessment.RiskLevel
	}
	if assessment.Reason != "" {
		updatedPlan["risk_reason"] = assessment.Reason
	}
	updatedPlan["checkpoint_required"] = assessment.CheckpointRequired
	return updatedPlan
}

func (s *Service) assessTaskGovernance(task runengine.TaskRecord, taskIntent map[string]any) (execution.GovernanceAssessment, bool, error) {
	if s.executor == nil {
		return execution.GovernanceAssessment{}, false, nil
	}
	resultTitle, _, _ := resultSpecFromIntent(taskIntent)
	return s.executor.AssessGovernance(context.Background(), execution.Request{
		TaskID:       task.TaskID,
		RunID:        task.RunID,
		SourceType:   task.SourceType,
		Title:        task.Title,
		Intent:       taskIntent,
		Snapshot:     snapshotFromTask(task),
		DeliveryType: resolveTaskDeliveryType(task, taskIntent),
		ResultTitle:  resultTitle,
	})
}

func (s *Service) handleTaskGovernanceDecision(task runengine.TaskRecord, taskIntent map[string]any) (runengine.TaskRecord, map[string]any, bool, error) {
	assessment, ok, err := s.assessTaskGovernance(task, taskIntent)
	if err != nil {
		return task, nil, false, err
	}
	if !ok {
		assessment, ok = s.fallbackGovernanceAssessment(task, taskIntent)
		if !ok {
			return task, nil, false, nil
		}
	}
	if assessment.Deny {
		response, blockedTask, blockErr := s.blockTaskByAssessment(task, assessment)
		return blockedTask, response, true, blockErr
	}
	if !assessment.ApprovalRequired {
		return task, nil, false, nil
	}
	pendingExecution := s.applyGovernanceAssessment(s.buildPendingExecution(task, taskIntent), assessment)
	approvalRequest := buildApprovalRequest(task.TaskID, taskIntent, assessment)
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "检测到待授权操作，请先确认。", task.UpdatedAt.Format(dateTimeLayout))
	updatedTask, changed := s.runEngine.MarkWaitingApprovalWithPlan(task.TaskID, approvalRequest, pendingExecution, bubble)
	if !changed {
		return task, nil, false, ErrTaskNotFound
	}
	if err := s.persistApprovalRequestState(updatedTask.TaskID, approvalRequest, assessment.ImpactScope); err != nil {
		return task, nil, false, err
	}
	return updatedTask, map[string]any{
		"task":            taskMap(updatedTask),
		"bubble_message":  bubble,
		"delivery_result": nil,
	}, true, nil
}

func (s *Service) fallbackGovernanceAssessment(task runengine.TaskRecord, taskIntent map[string]any) (execution.GovernanceAssessment, bool) {
	if stringValue(taskIntent, "name", "") != "write_file" && !boolValue(mapValue(taskIntent, "arguments"), "require_authorization", false) {
		return execution.GovernanceAssessment{}, false
	}
	plan := s.buildPendingExecution(task, taskIntent)
	impactScope := s.buildImpactScope(task, plan)
	return execution.GovernanceAssessment{
		OperationName:    firstNonEmptyString(stringValue(taskIntent, "name", ""), "write_file"),
		TargetObject:     impactScopeTarget(impactScope, targetPathFromIntent(taskIntent)),
		RiskLevel:        "red",
		ApprovalRequired: true,
		Reason:           "policy_requires_authorization",
		ImpactScope:      impactScope,
	}, true
}

func (s *Service) blockTaskByAssessment(task runengine.TaskRecord, assessment execution.GovernanceAssessment) (map[string]any, runengine.TaskRecord, error) {
	bubbleText := governanceInterceptionBubble(assessment)
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", bubbleText, task.UpdatedAt.Format(dateTimeLayout))
	updatedTask, ok := s.runEngine.BlockTaskByPolicy(task.TaskID, assessment.RiskLevel, bubbleText, assessment.ImpactScope, bubble)
	if !ok {
		return nil, task, ErrTaskNotFound
	}
	auditRecord := s.writeGovernanceAuditRecord(updatedTask.TaskID, updatedTask.RunID, "risk", "intercept_operation", bubbleText, impactScopeTarget(assessment.ImpactScope, assessment.TargetObject), "denied")
	updatedTask = s.appendAuditData(updatedTask, compactAuditRecords(auditRecord), nil)
	return map[string]any{
		"task":            taskMap(updatedTask),
		"bubble_message":  bubble,
		"delivery_result": nil,
		"impact_scope":    cloneMap(assessment.ImpactScope),
	}, updatedTask, nil
}

func (s *Service) writeGovernanceAuditRecord(taskID, runID, auditType, action, summary, target, result string) map[string]any {
	if s.audit == nil {
		return nil
	}
	if record, err := s.audit.Write(context.Background(), audit.RecordInput{
		TaskID:  taskID,
		Type:    auditType,
		Action:  action,
		Summary: summary,
		Target:  target,
		Result:  result,
	}); err == nil {
		return record.Map()
	}
	if record, err := s.audit.BuildRecord(audit.RecordInput{
		TaskID:  taskID,
		Type:    auditType,
		Action:  action,
		Summary: summary,
		Target:  target,
		Result:  result,
	}); err == nil {
		return record.Map()
	}
	return nil
}

func attachDeliveryResultToArtifacts(deliveryResult map[string]any, artifacts []map[string]any) []map[string]any {
	if len(artifacts) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		cloned := cloneMap(artifact)
		if cloned == nil {
			continue
		}
		if stringValue(cloned, "delivery_type", "") == "" {
			cloned["delivery_type"] = stringValue(deliveryResult, "type", "")
		}
		if len(mapValue(cloned, "delivery_payload")) == 0 {
			cloned["delivery_payload"] = cloneMap(mapValue(deliveryResult, "payload"))
		}
		if stringValue(cloned, "created_at", "") == "" {
			cloned["created_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		result = append(result, cloned)
	}
	return result
}

func (s *Service) persistArtifacts(taskID string, artifactPlans []map[string]any) {
	if s.storage == nil || s.storage.ArtifactStore() == nil || len(artifactPlans) == 0 {
		return
	}
	records := make([]storage.ArtifactRecord, 0, len(artifactPlans))
	for _, plan := range artifactPlans {
		records = append(records, storage.ArtifactRecord{
			ArtifactID:          stringValue(plan, "artifact_id", ""),
			TaskID:              firstNonEmptyString(stringValue(plan, "task_id", ""), taskID),
			ArtifactType:        stringValue(plan, "artifact_type", ""),
			Title:               stringValue(plan, "title", ""),
			Path:                stringValue(plan, "path", ""),
			MimeType:            stringValue(plan, "mime_type", ""),
			DeliveryType:        stringValue(plan, "delivery_type", ""),
			DeliveryPayloadJSON: stringValue(plan, "delivery_payload_json", "{}"),
			CreatedAt:           firstNonEmptyString(stringValue(plan, "created_at", ""), time.Now().UTC().Format(time.RFC3339)),
		})
	}
	_ = s.storage.ArtifactStore().SaveArtifacts(context.Background(), records)
	if task, ok := s.runEngine.GetTask(taskID); ok {
		merged := mergeArtifactsWithStored(task.Artifacts, s.loadArtifactsFromStorage(taskID, 0, 0))
		_, _ = s.runEngine.SetPresentation(taskID, task.BubbleMessage, task.DeliveryResult, merged)
	}
}

func (s *Service) artifactsForTask(taskID string, runtimeArtifacts []map[string]any) []map[string]any {
	return mergeArtifactsWithStored(delivery.EnsureArtifactIdentifiers(taskID, runtimeArtifacts), s.loadArtifactsFromStorage(taskID, 0, 0))
}

func (s *Service) citationsForTask(taskID string, runtimeCitations []map[string]any) []map[string]any {
	return mergeCitationsWithStored(s.loadTaskCitationsFromStorage(taskID), runtimeCitations)
}

func (s *Service) loadArtifactsFromStorage(taskID string, limit, offset int) []map[string]any {
	if s.storage == nil || s.storage.ArtifactStore() == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	records, _, err := s.storage.ArtifactStore().ListArtifacts(context.Background(), taskID, limit, offset)
	if err != nil {
		return nil
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, artifactMapFromStorage(record))
	}
	return items
}

func (s *Service) listArtifactsPage(taskID string, limit, offset int) ([]map[string]any, int, error) {
	if s.storage != nil && s.storage.ArtifactStore() != nil {
		records, total, err := s.storage.ArtifactStore().ListArtifacts(context.Background(), taskID, limit, offset)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
		}
		if total > 0 {
			items := make([]map[string]any, 0, len(records))
			for _, record := range records {
				items = append(items, artifactMapFromStorage(record))
			}
			return items, total, nil
		}
	}
	items := s.artifactsForTask(taskID, currentTaskArtifacts(s.runEngine, taskID))
	total := len(items)
	if offset >= total {
		return []map[string]any{}, total, nil
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return cloneMapSlice(items[offset:end]), total, nil
}

func currentTaskArtifacts(engine *runengine.Engine, taskID string) []map[string]any {
	if engine == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	task, ok := engine.GetTask(taskID)
	if !ok {
		return nil
	}
	return cloneMapSlice(task.Artifacts)
}

func (s *Service) findArtifactForTask(taskID, artifactID string) (map[string]any, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, ErrTaskNotFound
	}
	exists := false
	if task, ok := s.runEngine.GetTask(taskID); ok {
		exists = true
		for _, artifact := range delivery.EnsureArtifactIdentifiers(taskID, task.Artifacts) {
			if stringValue(artifact, "artifact_id", "") == artifactID {
				return cloneMap(artifact), nil
			}
		}
	}
	if !exists {
		if _, ok := s.taskDetailFromStorage(taskID); ok {
			exists = true
		}
	}
	if s.storage != nil && s.storage.ArtifactStore() != nil {
		records, _, err := s.storage.ArtifactStore().ListArtifacts(context.Background(), taskID, 0, 0)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStorageQueryFailed, err)
		}
		if len(records) > 0 {
			exists = true
		}
		for _, record := range records {
			if record.ArtifactID == artifactID {
				return artifactMapFromStorage(record), nil
			}
		}
	}
	if !exists {
		return nil, ErrTaskNotFound
	}
	return nil, ErrArtifactNotFound
}

func mergeArtifactsWithStored(runtimeArtifacts, storedArtifacts []map[string]any) []map[string]any {
	if len(runtimeArtifacts) == 0 && len(storedArtifacts) == 0 {
		return nil
	}
	merged := make([]map[string]any, 0, len(runtimeArtifacts)+len(storedArtifacts))
	seen := make(map[string]struct{})
	for _, group := range [][]map[string]any{storedArtifacts, runtimeArtifacts} {
		for _, artifact := range group {
			artifactID := stringValue(artifact, "artifact_id", "")
			if artifactID == "" {
				continue
			}
			if _, ok := seen[artifactID]; ok {
				continue
			}
			seen[artifactID] = struct{}{}
			merged = append(merged, cloneMap(artifact))
		}
	}
	return merged
}

func mergeCitationsWithStored(storedCitations, runtimeCitations []map[string]any) []map[string]any {
	if len(storedCitations) == 0 && len(runtimeCitations) == 0 {
		return nil
	}
	merged := make([]map[string]any, 0, len(storedCitations)+len(runtimeCitations))
	seen := make(map[string]struct{})
	for _, group := range [][]map[string]any{storedCitations, runtimeCitations} {
		for index, citation := range group {
			mergeKey := citationMergeKey(citation, index)
			if _, ok := seen[mergeKey]; ok {
				continue
			}
			seen[mergeKey] = struct{}{}
			merged = append(merged, cloneMap(citation))
		}
	}
	return merged
}

func citationMergeKey(citation map[string]any, index int) string {
	if citationID := strings.TrimSpace(stringValue(citation, "citation_id", "")); citationID != "" {
		return citationID
	}
	parts := []string{
		strings.TrimSpace(stringValue(citation, "task_id", "")),
		strings.TrimSpace(stringValue(citation, "source_ref", "")),
		strings.TrimSpace(stringValue(citation, "artifact_id", "")),
		strings.TrimSpace(stringValue(citation, "label", "")),
		strings.TrimSpace(stringValue(citation, "excerpt_text", "")),
	}
	key := strings.Join(parts, "|")
	if strings.Trim(key, "|") != "" {
		return key
	}
	return fmt.Sprintf("citation_%d", index)
}

func artifactMapFromStorage(record storage.ArtifactRecord) map[string]any {
	payload := map[string]any{}
	if strings.TrimSpace(record.DeliveryPayloadJSON) != "" {
		_ = json.Unmarshal([]byte(record.DeliveryPayloadJSON), &payload)
	}
	return map[string]any{
		"artifact_id":      record.ArtifactID,
		"task_id":          record.TaskID,
		"artifact_type":    record.ArtifactType,
		"title":            record.Title,
		"path":             record.Path,
		"mime_type":        record.MimeType,
		"delivery_type":    record.DeliveryType,
		"delivery_payload": payload,
		"created_at":       record.CreatedAt,
	}
}

func governanceInterceptionBubble(assessment execution.GovernanceAssessment) string {
	switch assessment.Reason {
	case risk.ReasonOutOfWorkspace:
		return "目标超出工作区边界，已阻止本次操作。"
	case risk.ReasonCommandNotAllowed:
		return "命令存在高危风险，已被策略拦截。"
	case risk.ReasonCapabilityDenied:
		return "当前平台能力不可用，已阻止本次操作。"
	default:
		return "高风险操作已被策略拦截，未进入执行。"
	}
}

func impactScopeTarget(impactScope map[string]any, fallback string) string {
	if files := stringSliceValue(impactScope["files"]); len(files) > 0 {
		return files[0]
	}
	return firstNonEmptyString(strings.TrimSpace(fallback), "main_flow")
}

// applyResolvedDeliveryToPlan folds the resolved task-level delivery preference
// back into a pending execution plan.
func (s *Service) applyResolvedDeliveryToPlan(task runengine.TaskRecord, plan map[string]any, taskIntent map[string]any) map[string]any {
	if len(plan) == 0 {
		return nil
	}

	updatedPlan := cloneMap(plan)
	deliveryType := resolveTaskDeliveryType(task, taskIntent)
	updatedPlan["delivery_type"] = deliveryType
	updatedPlan["preview_text"] = previewTextForDeliveryType(deliveryType)
	return updatedPlan
}

// resolveTaskDeliveryType computes the effective delivery type for a task.
func resolveTaskDeliveryType(task runengine.TaskRecord, taskIntent map[string]any) string {
	return resolveDeliveryType(task.PreferredDelivery, task.FallbackDelivery, deliveryTypeFromIntent(taskIntent))
}

// resolveDeliveryType resolves the final delivery type in priority order:
// task preference, fallback, then default.
func resolveDeliveryType(preferred, fallback, defaultType string) string {
	if normalized := normalizeDeliveryType(preferred); normalized != "" {
		return normalized
	}
	if strings.TrimSpace(preferred) != "" {
		if normalized := normalizeDeliveryType(fallback); normalized != "" {
			return normalized
		}
	}
	if normalized := normalizeDeliveryType(defaultType); normalized != "" {
		return normalized
	}
	if normalized := normalizeDeliveryType(fallback); normalized != "" {
		return normalized
	}
	return "workspace_document"
}

func normalizeDeliveryType(deliveryType string) string {
	switch deliveryType {
	case "bubble", "workspace_document":
		return deliveryType
	default:
		return ""
	}
}

// previewTextForDeliveryType returns the preview copy for each delivery type.
func previewTextForDeliveryType(deliveryType string) string {
	if deliveryType == "bubble" {
		return "\u7ed3\u679c\u5df2\u901a\u8fc7\u6c14\u6ce1\u8fd4\u56de"
	}
	return "\u5df2\u4e3a\u4f60\u5199\u5165\u6587\u6863\u5e76\u6253\u5f00"
}

// evaluateBudgetAutoDowngrade decides whether the visible budget setting should
// become a real execution downgrade before the task reaches model/tool work.
// The first P1 slice keeps the trigger set intentionally small and auditable:
// provider/API-key unavailability and token/cost pressure on the current task.
func (s *Service) evaluateBudgetAutoDowngrade(task runengine.TaskRecord, taskIntent map[string]any) budgetDowngradeDecision {
	modelSettings := modelSettingsSection(s.runEngine.Settings())
	modelCredentials := modelCredentialSettings(s.runEngine.Settings())
	if !boolValue(modelCredentials, "budget_auto_downgrade", true) {
		return budgetDowngradeDecision{}
	}
	policy := budgetPolicySettings(modelCredentials)
	decision := budgetDowngradeDecision{
		Enabled:      true,
		TriggerStage: "execution_preflight",
	}
	provider := providerFromSettings(modelSettings, model.OpenAIResponsesProvider)
	if !supportsBudgetProvider(provider) {
		decision.Applied = true
		decision.TriggerReason = "provider_unavailable"
		decision.DegradeActions = budgetDegradeActionsForReason(policy, "provider_unavailable")
		decision.Summary = "预算降级已生效：当前模型提供方不可用，任务改走轻量交付路径。"
		decision.Trace = buildBudgetDecisionTrace(task, decision, policy, 0, 0)
		return decision
	}
	failureSignals := recentBudgetFailureCount(task)
	if failureSignals >= intValue(policy, "failure_signal_window", 2) {
		decision.Applied = true
		decision.TriggerReason = "failure_pressure"
		decision.DegradeActions = budgetDegradeActionsForReason(policy, "failure_pressure")
		decision.Summary = "预算降级已生效：最近出现模型/提供方失败，任务改走轻量保守执行路径。"
		decision.Trace = buildBudgetDecisionTrace(task, decision, policy, failureSignals, 0)
		return decision
	}
	totalTokens := intValueFromAny(task.TokenUsage["total_tokens"])
	estimatedCost := floatValueFromAny(task.TokenUsage["estimated_cost"])
	if totalTokens >= intValue(policy, "token_pressure_threshold", 64) || estimatedCost >= floatValueFromAny(policy["cost_pressure_threshold"]) {
		decision.Applied = true
		decision.TriggerReason = "budget_pressure"
		decision.DegradeActions = budgetDegradeActionsForReason(policy, "budget_pressure")
		decision.Summary = "预算降级已生效：当前任务命中 token/成本压力，改为轻量交付并压缩上下文。"
		decision.Trace = buildBudgetDecisionTrace(task, decision, policy, failureSignals, map[string]any{"total_tokens": totalTokens, "estimated_cost": estimatedCost})
	}
	return decision
}

// applyBudgetAutoDowngrade mutates the execution request shape so the downgrade
// decision changes the real path instead of only updating settings summaries.
func (s *Service) applyBudgetAutoDowngrade(task runengine.TaskRecord, snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any, decision budgetDowngradeDecision) (runengine.TaskRecord, contextsvc.TaskContextSnapshot, map[string]any) {
	if !decision.Applied {
		return task, snapshot, taskIntent
	}
	updatedTask := task
	updatedTask.PreferredDelivery = "bubble"
	updatedTask.FallbackDelivery = "bubble"
	updatedIntent := cloneMap(taskIntent)
	arguments := cloneMap(mapValue(updatedIntent, "arguments"))
	if len(arguments) > 0 {
		if containsString(decision.DegradeActions, "skip_expensive_tools") {
			arguments["disable_tool_calls"] = true
		}
		arguments["budget_auto_downgrade_applied"] = true
		updatedIntent["arguments"] = arguments
	}
	updatedSnapshot := snapshot
	if containsString(decision.DegradeActions, "shrink_context") {
		updatedSnapshot.Text = truncateText(updatedSnapshot.Text, 160)
		updatedSnapshot.SelectionText = truncateText(updatedSnapshot.SelectionText, 160)
	}
	updatedTask.SecuritySummary = mergeBudgetDowngradeSummary(updatedTask.SecuritySummary, decision)
	return updatedTask, updatedSnapshot, updatedIntent
}

func mergeBudgetDowngradeSummary(current map[string]any, decision budgetDowngradeDecision) map[string]any {
	updated := cloneMap(current)
	if updated == nil {
		updated = map[string]any{}
	}
	updated["budget_auto_downgrade_applied"] = decision.Applied
	updated["budget_auto_downgrade_reason"] = decision.TriggerReason
	updated["budget_auto_downgrade_actions"] = append([]string(nil), decision.DegradeActions...)
	updated["budget_auto_downgrade_summary"] = decision.Summary
	updated["budget_auto_downgrade_trace"] = cloneMap(decision.Trace)
	return updated
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func supportsBudgetProvider(provider string) bool {
	switch strings.TrimSpace(provider) {
	case "", model.OpenAIResponsesProvider:
		return true
	default:
		return false
	}
}

func budgetPolicySettings(modelCredentials map[string]any) map[string]any {
	policy := cloneMap(mapValue(modelCredentials, "budget_policy"))
	if policy == nil {
		policy = map[string]any{}
	}
	if _, ok := policy["planner_retry_budget"]; !ok {
		policy["planner_retry_budget"] = 1
	}
	if _, ok := policy["failure_signal_window"]; !ok {
		policy["failure_signal_window"] = 2
	}
	if _, ok := policy["token_pressure_threshold"]; !ok {
		policy["token_pressure_threshold"] = 64
	}
	if _, ok := policy["cost_pressure_threshold"]; !ok {
		policy["cost_pressure_threshold"] = 0.05
	}
	if _, ok := policy["expensive_tool_categories"]; !ok {
		policy["expensive_tool_categories"] = []string{"command", "browser_mutation", "media_heavy"}
	}
	return policy
}

func budgetDegradeActionsForReason(policy map[string]any, reason string) []string {
	actions := []string{"lightweight_delivery"}
	switch reason {
	case "provider_unavailable", "failure_pressure":
		actions = append(actions, "skip_expensive_tools", "shrink_context")
	case "budget_pressure":
		actions = append(actions, "shrink_context")
	}
	if len(stringSliceValue(policy["expensive_tool_categories"])) > 0 && !containsString(actions, "skip_expensive_tools") && reason != "budget_pressure" {
		actions = append(actions, "skip_expensive_tools")
	}
	return actions
}

func buildBudgetDecisionTrace(task runengine.TaskRecord, decision budgetDowngradeDecision, policy map[string]any, failureSignals int, pressure any) map[string]any {
	return map[string]any{
		"task_id":                   task.TaskID,
		"run_id":                    task.RunID,
		"trigger_reason":            decision.TriggerReason,
		"trigger_stage":             decision.TriggerStage,
		"degrade_actions":           append([]string(nil), decision.DegradeActions...),
		"failure_signal_count":      failureSignals,
		"planner_retry_budget":      intValue(policy, "planner_retry_budget", 1),
		"failure_signal_window":     intValue(policy, "failure_signal_window", 2),
		"token_pressure_threshold":  intValue(policy, "token_pressure_threshold", 64),
		"cost_pressure_threshold":   floatValueFromAny(policy["cost_pressure_threshold"]),
		"expensive_tool_categories": stringSliceValue(policy["expensive_tool_categories"]),
		"pressure":                  pressure,
	}
}

func recentBudgetFailureCount(task runengine.TaskRecord) int {
	count := 0
	for _, record := range task.AuditRecords {
		if stringValue(record, "category", "") != "budget_auto_downgrade" {
			continue
		}
		if stringValue(record, "result", "") != "failed" {
			continue
		}
		count++
	}
	return count
}

func firstNonEmptyString(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func compactAuditRecords(records ...map[string]any) []map[string]any {
	if len(records) == 0 {
		return nil
	}

	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		items = append(items, cloneMap(record))
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func sameDay(left, right time.Time) bool {
	left = left.In(right.Location())
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}

func intValueFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func floatValueFromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0.0
	}
}

// firstMapOrNil returns a copy of the first item in a list, or nil when empty.
func firstMapOrNil(items []map[string]any) map[string]any {
	if len(items) == 0 {
		return nil
	}
	return cloneMap(items[0])
}

// latestRestorePointFromApprovals extracts the newest restore point carried by
// approval-derived task data.
func latestRestorePointFromApprovals(items []map[string]any) any {
	if len(items) == 0 {
		return nil
	}
	return map[string]any{
		"recovery_point_id": fmt.Sprintf("rp_%s", stringValue(items[0], "task_id", "latest")),
		"created_at":        time.Now().Format(dateTimeLayout),
	}
}

// cloneMap recursively copies a map[string]any payload.
func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			result[key] = cloneMap(typed)
		case []map[string]any:
			result[key] = cloneMapSlice(typed)
		case []string:
			result[key] = append([]string(nil), typed...)
		default:
			result[key] = value
		}
	}
	return result
}

// cloneMapSlice recursively copies a []map[string]any payload.
func cloneMapSlice(values []map[string]any) []map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, cloneMap(value))
	}
	return result
}

func extensionAssetReferencesFromMaps(values []map[string]any) []storage.ExtensionAssetReference {
	if len(values) == 0 {
		return nil
	}
	items := make([]storage.ExtensionAssetReference, 0, len(values))
	for _, value := range values {
		items = append(items, storage.ExtensionAssetReference{
			AssetKind:    stringValue(value, "asset_kind", ""),
			AssetID:      stringValue(value, "asset_id", ""),
			Name:         stringValue(value, "name", ""),
			Version:      stringValue(value, "version", ""),
			Source:       stringValue(value, "source", ""),
			Summary:      stringValue(value, "summary", ""),
			Entry:        stringValue(value, "entry", ""),
			Capabilities: stringSliceValue(value["capabilities"]),
			Permissions:  stringSliceValue(value["permissions"]),
			RuntimeNames: stringSliceValue(value["runtime_names"]),
		})
	}
	return items
}

// mapValue safely reads a nested object field.
func mapValue(values map[string]any, key string) map[string]any {
	rawValue, ok := values[key]
	if !ok {
		return map[string]any{}
	}
	value, ok := rawValue.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return value
}

// stringValue safely reads a string field and falls back when empty.
func stringValue(values map[string]any, key, fallback string) string {
	rawValue, ok := values[key]
	if !ok {
		return fallback
	}
	value, ok := rawValue.(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func requestTraceID(values map[string]any) string {
	return stringValue(mapValue(values, "request_meta"), "trace_id", "")
}

// boolValue safely reads a boolean field.
func boolValue(values map[string]any, key string, fallback bool) bool {
	rawValue, ok := values[key]
	if !ok {
		return fallback
	}
	value, ok := rawValue.(bool)
	if !ok {
		return fallback
	}
	return value
}

// intValue safely reads a JSON-decoded numeric field.
func intValue(values map[string]any, key string, fallback int) int {
	rawValue, ok := values[key]
	if !ok {
		return fallback
	}
	switch value := rawValue.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	default:
		return fallback
	}
}

// truncateText trims text to a fixed length for recommendation and memory
// query surfaces.
func truncateText(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength] + "..."
}

// dateTimeLayout is the shared timestamp layout exposed by orchestrator RPC
// payloads.
func (s *Service) executeTask(task runengine.TaskRecord, snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any) (runengine.TaskRecord, map[string]any, map[string]any, []map[string]any, error) {
	processingTask, ok := s.runEngine.BeginExecution(task.TaskID, executionStepName(taskIntent), "开始生成正式结果")
	if !ok {
		return runengine.TaskRecord{}, nil, nil, nil, ErrTaskNotFound
	}
	budgetDecision := s.evaluateBudgetAutoDowngrade(processingTask, taskIntent)
	processingTask, snapshot, taskIntent = s.applyBudgetAutoDowngrade(processingTask, snapshot, taskIntent, budgetDecision)
	if budgetDecision.Applied {
		_, _ = s.runEngine.UpdateSecuritySummary(processingTask.TaskID, processingTask.SecuritySummary)
	}

	resultTitle, _, resultBubbleText := resultSpecFromIntent(taskIntent)
	deliveryType := resolveTaskDeliveryType(processingTask, taskIntent)

	if s.executor == nil {
		deliveryResult := s.delivery.BuildDeliveryResultWithTargetPath(
			processingTask.TaskID,
			deliveryType,
			resultTitle,
			previewTextForDeliveryType(deliveryType),
			targetPathFromIntent(taskIntent),
		)
		artifacts := delivery.EnsureArtifactIdentifiers(processingTask.TaskID, s.delivery.BuildArtifact(processingTask.TaskID, resultTitle, deliveryResult))
		resultBubble := s.delivery.BuildBubbleMessage(processingTask.TaskID, "result", resultBubbleText, processingTask.UpdatedAt.Format(dateTimeLayout))
		auditRecords := compactAuditRecords(s.audit.BuildDeliveryAudit(processingTask.TaskID, processingTask.RunID, deliveryResult), s.buildBudgetDowngradeAudit(processingTask, budgetDecision))
		processingTask = s.appendAuditData(processingTask, auditRecords, nil)
		processingTask = s.recordBudgetDowngradeEvent(processingTask, budgetDecision)
		traceCapture, traceErr := s.captureExecutionTrace(processingTask, snapshot, taskIntent, execution.Result{
			Content:        previewTextForDeliveryType(deliveryType),
			DeliveryResult: deliveryResult,
			Artifacts:      artifacts,
		}, nil)
		if traceErr != nil {
			failedTask, failureBubble := s.failExecutionTask(processingTask, taskIntent, execution.Result{}, traceErr)
			return failedTask, failureBubble, nil, nil, nil
		}
		if escalatedTask, escalatedBubble, ok := s.maybeEscalateHumanLoop(processingTask, traceCapture); ok {
			return escalatedTask, escalatedBubble, nil, nil, nil
		}
		updatedTask, ok := s.runEngine.CompleteTask(processingTask.TaskID, deliveryResult, resultBubble, artifacts)
		if !ok {
			return runengine.TaskRecord{}, nil, nil, nil, ErrTaskNotFound
		}
		updatedTask = s.attachFormalCitations(processingTask, updatedTask, nil, nil, deliveryResult, artifacts)
		s.attachPostDeliveryHandoffs(updatedTask.TaskID, updatedTask.RunID, snapshot, taskIntent, deliveryResult, artifacts)
		return updatedTask, resultBubble, deliveryResult, artifacts, nil
	}

	approvedOperation, approvedTargetObject := approvedExecutionFromTask(processingTask)
	executionResult, err := s.executor.Execute(context.Background(), execution.Request{
		TaskID:               processingTask.TaskID,
		RunID:                processingTask.RunID,
		SourceType:           processingTask.SourceType,
		Title:                processingTask.Title,
		Intent:               taskIntent,
		AttemptIndex:         executionAttemptIndex(task, processingTask),
		SegmentKind:          executionSegmentKind(task, processingTask),
		Snapshot:             snapshot,
		SteeringMessages:     append([]string(nil), processingTask.SteeringMessages...),
		DeliveryType:         deliveryType,
		ResultTitle:          resultTitle,
		ApprovalGranted:      processingTask.Authorization != nil,
		ApprovedOperation:    approvedOperation,
		ApprovedTargetObject: approvedTargetObject,
		BudgetDowngrade: map[string]any{
			"enabled":         budgetDecision.Enabled,
			"applied":         budgetDecision.Applied,
			"trigger_reason":  budgetDecision.TriggerReason,
			"trigger_stage":   budgetDecision.TriggerStage,
			"degrade_actions": append([]string(nil), budgetDecision.DegradeActions...),
			"summary":         budgetDecision.Summary,
			"trace":           cloneMap(budgetDecision.Trace),
		},
	})
	processingTask = s.recordExecutionToolCalls(processingTask, executionResult.ToolCalls)
	s.persistExecutionToolCallEvents(processingTask, taskIntent, executionResult.ToolCalls)
	auditDeliveryResult := executionResult.DeliveryResult
	if err != nil {
		auditDeliveryResult = nil
	}
	executionAuditRecords, executionTokenUsage := s.buildExecutionAudit(processingTask, executionResult.ToolCalls, auditDeliveryResult)
	if len(executionResult.BudgetFailure) > 0 {
		executionAuditRecords = append(executionAuditRecords, cloneMap(executionResult.BudgetFailure))
	}
	executionAuditRecords = append(executionAuditRecords, s.buildBudgetDowngradeAudit(processingTask, budgetDecision))
	processingTask = s.appendAuditData(processingTask, executionAuditRecords, executionTokenUsage)
	processingTask = s.recordBudgetDowngradeEvent(processingTask, budgetDecision)
	traceCapture, traceErr := s.captureExecutionTrace(processingTask, snapshot, taskIntent, executionResult, err)
	if traceErr != nil {
		failedTask, failureBubble := s.failExecutionTask(processingTask, taskIntent, executionResult, traceErr)
		return failedTask, failureBubble, nil, nil, nil
	}
	if escalatedTask, escalatedBubble, ok := s.maybeEscalateHumanLoop(processingTask, traceCapture, executionResult); ok {
		return escalatedTask, escalatedBubble, nil, nil, nil
	}
	if err != nil {
		failedTask, failureBubble := s.failExecutionTask(processingTask, taskIntent, executionResult, err)
		return failedTask, failureBubble, nil, nil, nil
	}

	resultBubble := s.delivery.BuildBubbleMessage(
		processingTask.TaskID,
		"result",
		firstNonEmptyString(executionResult.BubbleText, resultBubbleText),
		processingTask.UpdatedAt.Format(dateTimeLayout),
	)
	executionArtifacts := delivery.EnsureArtifactIdentifiers(processingTask.TaskID, executionResult.Artifacts)
	updatedTask, ok := s.runEngine.CompleteTask(processingTask.TaskID, executionResult.DeliveryResult, resultBubble, executionArtifacts, executionResult.RecoveryPoint)
	if !ok {
		return runengine.TaskRecord{}, nil, nil, nil, ErrTaskNotFound
	}
	s.persistExecutionDeliveryResult(updatedTask, taskIntent, executionResult.DeliveryResult)
	updatedTask = s.attachFormalCitations(processingTask, updatedTask, executionResult.ToolCalls, executionResult.ToolOutput, executionResult.DeliveryResult, executionArtifacts)
	s.attachPostDeliveryHandoffs(updatedTask.TaskID, updatedTask.RunID, snapshot, taskIntent, executionResult.DeliveryResult, executionArtifacts)
	return updatedTask, resultBubble, executionResult.DeliveryResult, executionArtifacts, nil
}

// attachFormalCitations upgrades execution-side citation seeds into protocol-facing
// citation objects so task detail can expose stable evidence references without
// leaking raw tool outputs or worker-only payloads.
func (s *Service) attachFormalCitations(sourceTask runengine.TaskRecord, persistedTask runengine.TaskRecord, toolCalls []tools.ToolCallRecord, toolOutput map[string]any, deliveryResult map[string]any, artifacts []map[string]any) runengine.TaskRecord {
	citations := buildTaskCitations(sourceTask, toolCalls, toolOutput, deliveryResult, artifacts)
	s.persistFormalCitations(persistedTask.TaskID, citations)
	if _, ok := s.runEngine.SetCitations(persistedTask.TaskID, citations); ok {
		if updatedTask, exists := s.runEngine.GetTask(persistedTask.TaskID); exists {
			return updatedTask
		}
	}
	return persistedTask
}

// persistFormalCitations keeps the first-class citation chain queryable even
// after task_run compatibility snapshots have been compacted away.
func (s *Service) persistFormalCitations(taskID string, citations []map[string]any) {
	if s == nil || s.storage == nil || s.storage.LoopRuntimeStore() == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	records := make([]storage.CitationRecord, 0, len(citations))
	for index, citation := range citations {
		records = append(records, storage.CitationRecord{
			CitationID:      stringValue(citation, "citation_id", ""),
			TaskID:          firstNonEmptyString(stringValue(citation, "task_id", ""), taskID),
			RunID:           stringValue(citation, "run_id", ""),
			SourceType:      stringValue(citation, "source_type", "context"),
			SourceRef:       stringValue(citation, "source_ref", ""),
			Label:           stringValue(citation, "label", ""),
			ArtifactID:      stringValue(citation, "artifact_id", ""),
			ArtifactType:    stringValue(citation, "artifact_type", ""),
			EvidenceRole:    stringValue(citation, "evidence_role", ""),
			ExcerptText:     stringValue(citation, "excerpt_text", ""),
			ScreenSessionID: stringValue(citation, "screen_session_id", ""),
			OrderIndex:      index,
		})
	}
	_ = s.storage.LoopRuntimeStore().ReplaceTaskCitations(context.Background(), taskID, records)
}

func buildTaskCitations(task runengine.TaskRecord, toolCalls []tools.ToolCallRecord, toolOutput map[string]any, deliveryResult map[string]any, artifacts []map[string]any) []map[string]any {
	citations := make([]map[string]any, 0)
	seen := make(map[string]struct{})
	artifactsByID := make(map[string]map[string]any, len(artifacts))
	for _, artifact := range artifacts {
		artifactID := stringValue(artifact, "artifact_id", "")
		if strings.TrimSpace(artifactID) != "" {
			artifactsByID[artifactID] = cloneMap(artifact)
		}
	}
	for _, call := range toolCalls {
		seed := mapValue(call.Output, "citation_seed")
		if len(seed) == 0 {
			continue
		}
		citation := citationFromSeed(task, seed, artifactsByID, deliveryResult)
		if len(citation) == 0 {
			continue
		}
		citationID := stringValue(citation, "citation_id", "")
		if _, ok := seen[citationID]; ok {
			continue
		}
		seen[citationID] = struct{}{}
		citations = append(citations, citation)
	}
	if seed := mapValue(toolOutput, "citation_seed"); len(seed) > 0 {
		citation := citationFromSeed(task, seed, artifactsByID, deliveryResult)
		if len(citation) > 0 {
			citationID := stringValue(citation, "citation_id", "")
			if _, ok := seen[citationID]; !ok {
				seen[citationID] = struct{}{}
				citations = append(citations, citation)
			}
		}
	}
	if latestSeed := mapValue(task.LatestToolCall, "output"); len(latestSeed) > 0 {
		seed := mapValue(latestSeed, "citation_seed")
		if len(seed) > 0 {
			citation := citationFromSeed(task, seed, artifactsByID, deliveryResult)
			if len(citation) > 0 {
				citationID := stringValue(citation, "citation_id", "")
				if _, ok := seen[citationID]; !ok {
					citations = append(citations, citation)
				}
			}
		}
	}
	return citations
}

func citationFromSeed(task runengine.TaskRecord, seed map[string]any, artifactsByID map[string]map[string]any, deliveryResult map[string]any) map[string]any {
	artifactID := stringValue(seed, "artifact_id", "")
	artifactType := stringValue(seed, "artifact_type", "")
	evidenceRole := stringValue(seed, "evidence_role", "")
	ocrExcerpt := stringValue(seed, "ocr_excerpt", "")
	sourceRef := firstNonEmptyString(artifactID, stringValue(seed, "screen_session_id", ""))
	if strings.TrimSpace(sourceRef) == "" {
		sourceRef = stringValue(mapValue(deliveryResult, "payload"), "task_id", task.TaskID)
	}
	labelParts := make([]string, 0, 3)
	if strings.TrimSpace(evidenceRole) != "" {
		labelParts = append(labelParts, evidenceRole)
	}
	if strings.TrimSpace(artifactType) != "" {
		labelParts = append(labelParts, artifactType)
	}
	if strings.TrimSpace(ocrExcerpt) != "" {
		labelParts = append(labelParts, truncateText(ocrExcerpt, 64))
	}
	label := strings.Join(labelParts, " | ")
	if strings.TrimSpace(label) == "" {
		label = "screen evidence"
	}
	sourceType := "context"
	if _, ok := artifactsByID[artifactID]; ok {
		sourceType = "file"
	}
	identity := stableCitationIdentity(task.TaskID, sourceType, sourceRef, seed)
	result := map[string]any{
		"citation_id": fmt.Sprintf("cit_%s_%s", task.TaskID, identity),
		"task_id":     task.TaskID,
		"run_id":      task.RunID,
		"source_type": sourceType,
		"source_ref":  sourceRef,
		"label":       label,
	}
	if strings.TrimSpace(artifactID) != "" {
		result["artifact_id"] = artifactID
	}
	if strings.TrimSpace(artifactType) != "" {
		result["artifact_type"] = artifactType
	}
	if strings.TrimSpace(evidenceRole) != "" {
		result["evidence_role"] = evidenceRole
	}
	if strings.TrimSpace(ocrExcerpt) != "" {
		result["excerpt_text"] = ocrExcerpt
	}
	if screenSessionID := strings.TrimSpace(stringValue(seed, "screen_session_id", "")); screenSessionID != "" {
		result["screen_session_id"] = screenSessionID
	}
	return result
}

// stableCitationIdentity derives a deterministic citation fingerprint from the
// full formal seed so identical seeds collapse while distinct references on the
// same artifact remain separately addressable.
func stableCitationIdentity(taskID, sourceType, sourceRef string, seed map[string]any) string {
	normalized := map[string]any{
		"task_id":           taskID,
		"source_type":       strings.TrimSpace(sourceType),
		"source_ref":        strings.TrimSpace(sourceRef),
		"artifact_id":       strings.TrimSpace(stringValue(seed, "artifact_id", "")),
		"artifact_type":     strings.TrimSpace(stringValue(seed, "artifact_type", "")),
		"evidence_role":     strings.TrimSpace(stringValue(seed, "evidence_role", "")),
		"ocr_excerpt":       strings.TrimSpace(stringValue(seed, "ocr_excerpt", "")),
		"screen_session_id": strings.TrimSpace(stringValue(seed, "screen_session_id", "")),
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "evidence"
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:8])
}

func executionAttemptIndex(previousTask, processingTask runengine.TaskRecord) int {
	if processingTask.ExecutionAttempt > 0 {
		return processingTask.ExecutionAttempt
	}
	if previousTask.ExecutionAttempt > 0 {
		if strings.TrimSpace(previousTask.RunID) == "" || previousTask.RunID == processingTask.RunID {
			return previousTask.ExecutionAttempt
		}
		return previousTask.ExecutionAttempt + 1
	}
	if strings.TrimSpace(previousTask.RunID) == "" || previousTask.RunID == processingTask.RunID {
		return 1
	}
	return 2
}

func executionSegmentKind(previousTask, processingTask runengine.TaskRecord) string {
	if strings.TrimSpace(previousTask.RunID) != "" && previousTask.RunID != processingTask.RunID {
		return executionSegmentRestart
	}
	if previousTask.Status == "paused" || taskIsBlockedHumanLoop(previousTask) {
		return executionSegmentResume
	}
	return executionSegmentInitial
}

// dateTimeLayout is the shared timestamp layout exposed by orchestrator RPC
// payloads.

func (s *Service) captureExecutionTrace(task runengine.TaskRecord, snapshot contextsvc.TaskContextSnapshot, taskIntent map[string]any, result execution.Result, executionErr error) (traceeval.CaptureResult, error) {
	if s.traceEval == nil {
		return traceeval.CaptureResult{}, nil
	}
	capture, err := s.traceEval.Capture(traceeval.CaptureInput{
		TaskID:          task.TaskID,
		RunID:           task.RunID,
		IntentName:      stringValue(taskIntent, "name", ""),
		Snapshot:        snapshot,
		OutputText:      result.Content,
		DeliveryResult:  cloneMap(result.DeliveryResult),
		Artifacts:       cloneMapSlice(result.Artifacts),
		ExtensionAssets: extensionAssetReferencesFromMaps(result.ExtensionAssets),
		ModelInvocation: cloneMap(result.ModelInvocation),
		ToolCalls:       append([]tools.ToolCallRecord(nil), result.ToolCalls...),
		TokenUsage:      cloneMap(task.TokenUsage),
		DurationMS:      result.DurationMS,
		ExecutionError:  executionErr,
	})
	if err != nil {
		return traceeval.CaptureResult{}, err
	}
	if err := s.traceEval.Record(context.Background(), capture); err != nil {
		return traceeval.CaptureResult{}, err
	}
	return capture, nil
}

func (s *Service) resumeHumanLoopTask(task runengine.TaskRecord, reviewDecision map[string]any) (runengine.TaskRecord, map[string]any, map[string]any, bool, error) {
	if !resumedFromHumanLoop(task) {
		return runengine.TaskRecord{}, nil, nil, false, nil
	}
	pendingExecution, ok := s.runEngine.PendingExecutionPlan(task.TaskID)
	if !ok {
		return runengine.TaskRecord{}, nil, nil, false, nil
	}
	escalation := mapValue(pendingExecution, "escalation")
	if len(escalation) == 0 {
		return runengine.TaskRecord{}, nil, nil, false, nil
	}
	decision := strings.TrimSpace(stringValue(reviewDecision, "decision", ""))
	if decision == "" {
		return runengine.TaskRecord{}, nil, nil, false, fmt.Errorf("review.decision is required for human review resume")
	}
	if decision != "approve" && decision != "replan" {
		return runengine.TaskRecord{}, nil, nil, false, fmt.Errorf("unsupported review decision: %s", decision)
	}
	escalation["review_result"] = decision
	escalation["reviewed_at"] = currentTimeFromTask(s.runEngine, task.TaskID)
	if reviewerID := strings.TrimSpace(stringValue(reviewDecision, "reviewer_id", "")); reviewerID != "" {
		escalation["reviewer_id"] = reviewerID
	}
	if notes := strings.TrimSpace(stringValue(reviewDecision, "notes", "")); notes != "" {
		escalation["review_notes"] = notes
	}
	if correctedIntent := mapValue(reviewDecision, "corrected_intent"); len(correctedIntent) > 0 {
		escalation["corrected_intent"] = cloneMap(correctedIntent)
	}
	suggestedAction := firstNonEmptyString(stringValue(escalation, "suggested_action", ""), "review_and_replan")
	if suggestedAction != "review_and_replan" {
		return runengine.TaskRecord{}, nil, nil, false, nil
	}
	if decision == "replan" {
		intentValue := cloneMap(task.Intent)
		if correctedIntent := mapValue(escalation, "corrected_intent"); len(correctedIntent) > 0 {
			intentValue = correctedIntent
		}
		updatedTitle := s.intent.Suggest(snapshotFromTask(task), intentValue, false).TaskTitle
		replanBubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "人工复核要求重新规划，请确认新的处理意图。", task.UpdatedAt.Format(dateTimeLayout))
		replannedTask, ok := s.runEngine.ReopenIntentConfirmation(task.TaskID, updatedTitle, intentValue, replanBubble)
		if !ok {
			return runengine.TaskRecord{}, nil, nil, false, ErrTaskNotFound
		}
		return replannedTask, replanBubble, nil, true, nil
	}
	resultBubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", "人工复核完成，任务继续执行。", task.UpdatedAt.Format(dateTimeLayout))
	updatedTask, bubble, deliveryResult, _, err := s.executeTask(task, snapshotFromTask(task), task.Intent)
	if err != nil {
		return runengine.TaskRecord{}, nil, nil, false, err
	}
	if bubble == nil {
		bubble = resultBubble
	}
	return updatedTask, bubble, deliveryResult, true, nil
}

func (s *Service) maybeEscalateHumanLoop(task runengine.TaskRecord, capture traceeval.CaptureResult, executionResult ...execution.Result) (runengine.TaskRecord, map[string]any, bool) {
	if capture.HumanInLoop == nil {
		return runengine.TaskRecord{}, nil, false
	}
	if len(executionResult) > 0 && executionAttemptHasSideEffects(executionResult[0]) {
		return runengine.TaskRecord{}, nil, false
	}
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", capture.HumanInLoop.Summary, task.UpdatedAt.Format(dateTimeLayout))
	escalation := map[string]any{
		"escalation_id":    capture.HumanInLoop.EscalationID,
		"reason":           capture.HumanInLoop.Reason,
		"review_result":    capture.HumanInLoop.ReviewResult,
		"status":           capture.HumanInLoop.Status,
		"summary":          capture.HumanInLoop.Summary,
		"suggested_action": capture.HumanInLoop.SuggestedAction,
		"created_at":       capture.HumanInLoop.CreatedAt,
	}
	updatedTask, ok := s.runEngine.EscalateHumanLoop(task.TaskID, escalation, bubble)
	if !ok {
		return runengine.TaskRecord{}, nil, false
	}
	return updatedTask, bubble, true
}

func resumedFromHumanLoop(task runengine.TaskRecord) bool {
	if task.Status != "processing" || task.CurrentStep != executionStepName(task.Intent) {
		return false
	}
	return true
}

func executionAttemptHasSideEffects(result execution.Result) bool {
	if len(result.ToolCalls) == 0 {
		return false
	}
	for _, toolCall := range result.ToolCalls {
		if !isMutatingToolCall(toolCall.ToolName) {
			continue
		}
		return true
	}
	return false
}

func isMutatingToolCall(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "write_file", "exec_command", "page_interact", "transcode_media", "normalize_recording", "extract_frames":
		return true
	default:
		return false
	}
}

func (s *Service) recordExecutionToolCalls(task runengine.TaskRecord, toolCalls []tools.ToolCallRecord) runengine.TaskRecord {
	for _, toolCall := range toolCalls {
		if toolCall.ToolName == "" {
			continue
		}
		if recordedTask, ok := s.runEngine.RecordToolCallLifecycle(
			task.TaskID,
			toolCall.ToolName,
			string(toolCall.Status),
			toolCall.Input,
			toolCall.Output,
			toolCall.DurationMS,
			toolCallErrorCode(toolCall),
		); ok {
			task = recordedTask
		}
	}
	return task
}

func (s *Service) persistExecutionToolCallEvents(task runengine.TaskRecord, taskIntent map[string]any, toolCalls []tools.ToolCallRecord) {
	if s == nil || s.storage == nil || s.storage.LoopRuntimeStore() == nil || isAgentLoopTaskIntent(taskIntent) || len(toolCalls) == 0 {
		return
	}
	startedAt := time.Now().UTC()
	records := make([]storage.EventRecord, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.ToolName) == "" {
			continue
		}
		createdAt := startedAt.Add(time.Duration(index) * time.Millisecond)
		records = append(records, storage.EventRecord{
			EventID:     executionToolCallEventID(task.TaskID, toolCall, index, createdAt),
			RunID:       task.RunID,
			TaskID:      task.TaskID,
			StepID:      toolCall.StepID,
			Type:        "tool_call.completed",
			Level:       executionToolCallEventLevel(toolCall),
			PayloadJSON: marshalOrchestratorEventPayload(executionToolCallEventPayload(task.TaskID, toolCall)),
			CreatedAt:   createdAt.Format(time.RFC3339Nano),
		})
	}
	if len(records) == 0 {
		return
	}
	_ = s.storage.LoopRuntimeStore().SaveEvents(context.Background(), records)
}

func executionToolCallEventID(taskID string, toolCall tools.ToolCallRecord, index int, createdAt time.Time) string {
	if sanitizedToolCallID := strings.TrimSpace(strings.ReplaceAll(toolCall.ToolCallID, ".", "_")); sanitizedToolCallID != "" {
		return fmt.Sprintf("evt_%s_%s_%d", taskID, sanitizedToolCallID, index)
	}
	sanitizedToolName := strings.TrimSpace(strings.ReplaceAll(toolCall.ToolName, ".", "_"))
	if sanitizedToolName == "" {
		sanitizedToolName = "tool_call"
	}
	sanitizedStepID := strings.TrimSpace(strings.ReplaceAll(toolCall.StepID, ".", "_"))
	if sanitizedStepID == "" {
		sanitizedStepID = "task_scope"
	}
	return fmt.Sprintf("evt_%s_%s_%s_%d_%d_%d", taskID, sanitizedToolName, sanitizedStepID, index, createdAt.UnixNano(), persistedToolCallEventSeq.Add(1))
}

func (s *Service) persistExecutionDeliveryResult(task runengine.TaskRecord, taskIntent map[string]any, deliveryResult map[string]any) {
	if s == nil || s.storage == nil || s.storage.LoopRuntimeStore() == nil || isAgentLoopTaskIntent(taskIntent) || len(deliveryResult) == 0 {
		return
	}
	createdAt := time.Now().UTC()
	deliveryResultID := fmt.Sprintf("delivery_result_%s_%d", task.TaskID, createdAt.UnixNano())
	payloadJSON := marshalOrchestratorEventPayload(mapValue(deliveryResult, "payload"))
	_ = s.storage.LoopRuntimeStore().SaveDeliveryResult(context.Background(), storage.DeliveryResultRecord{
		DeliveryResultID: deliveryResultID,
		TaskID:           task.TaskID,
		Type:             stringValue(deliveryResult, "type", "bubble"),
		Title:            stringValue(deliveryResult, "title", ""),
		PayloadJSON:      payloadJSON,
		PreviewText:      stringValue(deliveryResult, "preview_text", ""),
		CreatedAt:        createdAt.Format(time.RFC3339Nano),
	})
	_ = s.storage.LoopRuntimeStore().SaveEvents(context.Background(), []storage.EventRecord{{
		EventID:     fmt.Sprintf("evt_%s_delivery_ready_%d", task.TaskID, createdAt.UnixNano()),
		RunID:       task.RunID,
		TaskID:      task.TaskID,
		Type:        "delivery.ready",
		Level:       "info",
		PayloadJSON: marshalOrchestratorEventPayload(executionDeliveryReadyPayload(task.TaskID, deliveryResultID, deliveryResult)),
		CreatedAt:   createdAt.Add(time.Millisecond).Format(time.RFC3339Nano),
	}})
}

func executionToolCallEventLevel(toolCall tools.ToolCallRecord) string {
	switch toolCall.Status {
	case tools.ToolCallStatusFailed, tools.ToolCallStatusTimeout:
		return "error"
	default:
		return "info"
	}
}

func executionToolCallEventPayload(taskID string, toolCall tools.ToolCallRecord) map[string]any {
	payload := map[string]any{
		"task_id":      taskID,
		"tool_call_id": toolCall.ToolCallID,
		"tool_name":    toolCall.ToolName,
		"status":       string(toolCall.Status),
		"tool_status":  string(toolCall.Status),
		"input":        cloneMapOrEmpty(toolCall.Input),
		"output":       cloneMapOrEmpty(toolCall.Output),
		"duration_ms":  toolCall.DurationMS,
	}
	if strings.TrimSpace(toolCall.StepID) != "" {
		payload["step_id"] = toolCall.StepID
	}
	if toolCall.ErrorCode != nil {
		payload["error_code"] = *toolCall.ErrorCode
	}
	for _, key := range []string{"path", "url", "output_path", "output_dir", "source", "execution_backend", "page_count", "frame_count"} {
		if value, ok := toolCall.Output[key]; ok {
			payload[key] = value
			continue
		}
		if value, ok := toolCall.Input[key]; ok {
			payload[key] = value
		}
	}
	if summaryOutput, ok := toolCall.Output["summary_output"].(map[string]any); ok && len(summaryOutput) > 0 {
		payload["summary_output"] = cloneMap(summaryOutput)
	}
	return payload
}

func executionDeliveryReadyPayload(taskID, deliveryResultID string, deliveryResult map[string]any) map[string]any {
	payload := map[string]any{
		"task_id":            taskID,
		"delivery_result_id": deliveryResultID,
		"delivery_type":      stringValue(deliveryResult, "type", "bubble"),
		"preview_text":       stringValue(deliveryResult, "preview_text", ""),
	}
	deliveryPayload := mapValue(deliveryResult, "payload")
	for _, key := range []string{"path", "url"} {
		if value, ok := deliveryPayload[key]; ok {
			payload[key] = value
		}
	}
	return payload
}

func marshalOrchestratorEventPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func isAgentLoopTaskIntent(taskIntent map[string]any) bool {
	return stringValue(taskIntent, "name", "") == "agent_loop"
}

func executionStepName(taskIntent map[string]any) string {
	if stringValue(taskIntent, "name", "") == "agent_loop" {
		return "agent_loop"
	}
	return "generate_output"
}

func approvedExecutionFromTask(task runengine.TaskRecord) (string, string) {
	if len(task.PendingExecution) == 0 {
		return "", ""
	}
	return stringValue(task.PendingExecution, "operation_name", ""), stringValue(task.PendingExecution, "target_object", "")
}

func toolCallErrorCode(toolCall tools.ToolCallRecord) any {
	if toolCall.ErrorCode == nil {
		return nil
	}
	return *toolCall.ErrorCode
}

func (s *Service) failExecutionTask(task runengine.TaskRecord, taskIntent map[string]any, executionResult execution.Result, err error) (runengine.TaskRecord, map[string]any) {
	impactScope := s.buildImpactScope(task, task.PendingExecution)
	bubbleText := executionFailureBubble(err)
	securityStatus := "execution_error"
	stepName := "execution_failed"
	auditType := "execution"
	auditAction := "execute_task"
	auditTarget := impactScopeTarget(impactScope, targetPathFromIntent(taskIntent))
	auditResult := "failed"
	failureCode, failureCategory := classifyExecutionFailure(task, err)
	if errors.Is(err, execution.ErrRecoveryPointPrepareFailed) {
		securityStatus = "execution_error"
		stepName = "recovery_prepare_failed"
		auditType = "recovery"
		auditAction = "create_recovery_point"
		auditTarget = impactScopeTarget(impactScope, stringValue(executionResult.RecoveryPoint, "summary", "workspace"))
	}
	bubble := s.delivery.BuildBubbleMessage(task.TaskID, "status", bubbleText, task.UpdatedAt.Format(dateTimeLayout))
	updatedTask, ok := s.runEngine.FailTaskExecution(task.TaskID, stepName, securityStatus, bubbleText, impactScope, bubble, executionResult.RecoveryPoint)
	if !ok {
		return task, bubble
	}
	updatedTask = s.attachFormalCitations(task, updatedTask, executionResult.ToolCalls, executionResult.ToolOutput, executionResult.DeliveryResult, executionResult.Artifacts)
	auditRecord := s.writeGovernanceAuditRecord(updatedTask.TaskID, updatedTask.RunID, auditType, auditAction, bubbleText, auditTarget, auditResult)
	if len(auditRecord) > 0 {
		metadata := cloneMap(mapValue(auditRecord, "metadata"))
		if metadata == nil {
			metadata = map[string]any{}
		}
		if failureCode != "" {
			metadata["failure_code"] = failureCode
		}
		if failureCategory != "" {
			metadata["failure_category"] = failureCategory
		}
		if len(metadata) > 0 {
			auditRecord["metadata"] = metadata
		}
	}
	budgetFailureAudit := s.buildBudgetFailureAudit(updatedTask, err)
	updatedTask = s.appendAuditData(updatedTask, compactAuditRecords(auditRecord, budgetFailureAudit), nil)
	return updatedTask, bubble
}

// classifyExecutionFailure keeps task-facing runtime summaries and governance
// metadata aligned with the formal protocol error names without exposing raw
// provider or worker errors as long-term UI contracts.
func classifyExecutionFailure(task runengine.TaskRecord, err error) (string, string) {
	if failureCode, failureCategory := classifyScreenFailure(task, err); failureCode != "" || failureCategory != "" {
		return failureCode, failureCategory
	}
	return classifyModelFailure(err)
}

// classifyScreenFailure keeps screen-task runtime summaries and governance
// metadata aligned with the formal protocol error names while still exposing a
// task-facing failure category for UI grouping.
func classifyScreenFailure(task runengine.TaskRecord, err error) (string, string) {
	if stringValue(task.Intent, "name", "") != "screen_analyze" && task.SourceType != "screen_capture" {
		return "", ""
	}
	lowerError := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, tools.ErrApprovalRequired), errors.Is(err, tools.ErrScreenCaptureUnauthorized):
		return "APPROVAL_REQUIRED", "screen_authorization"
	case errors.Is(err, tools.ErrScreenCaptureNotSupported):
		return "PLATFORM_NOT_SUPPORTED", "screen_capability"
	case errors.Is(err, tools.ErrOCRWorkerFailed):
		return "OCR_WORKER_FAILED", "screen_ocr"
	case errors.Is(err, tools.ErrMediaWorkerFailed):
		return "MEDIA_WORKER_FAILED", "screen_media"
	case errors.Is(err, tools.ErrPlaywrightSidecarFailed), errors.Is(err, tools.ErrScreenCaptureFailed), errors.Is(err, tools.ErrScreenKeyframeSamplingFailed):
		return "PLAYWRIGHT_SIDECAR_FAILED", "screen_capture"
	case errors.Is(err, tools.ErrCapabilityDenied):
		return "CAPABILITY_DENIED", "screen_capability"
	case errors.Is(err, tools.ErrToolOutputInvalid):
		return "TOOL_OUTPUT_INVALID", "screen_observation"
	case errors.Is(err, tools.ErrScreenCaptureSessionExpired), strings.Contains(lowerError, "session"):
		return "TOOL_EXECUTION_FAILED", "screen_session"
	case strings.Contains(lowerError, "incomplete") || strings.Contains(lowerError, "empty") || strings.Contains(lowerError, "未识别"):
		return "TOOL_OUTPUT_INVALID", "screen_observation"
	default:
		return "TOOL_EXECUTION_FAILED", "screen_analysis"
	}
}

// classifyModelFailure normalizes formal model-provider failures into stable
// protocol codes so task detail and runtime summaries can expose one canonical
// failure contract instead of transport-specific error strings.
func classifyModelFailure(err error) (string, string) {
	switch {
	case errors.Is(err, model.ErrModelProviderUnsupported):
		return "MODEL_PROVIDER_NOT_FOUND", "model_provider"
	case errors.Is(err, model.ErrModelProviderRequired):
		return "MODEL_PROVIDER_NOT_FOUND", "model_provider"
	case model.IsProviderRuntimeUnavailable(err):
		return "MODEL_RUNTIME_UNAVAILABLE", "model_runtime"
	case errors.Is(err, model.ErrToolCallingNotSupported):
		return "MODEL_NOT_ALLOWED", "model_capability"
	case errors.Is(err, model.ErrOpenAIEndpointRequired), errors.Is(err, model.ErrOpenAIModelIDRequired), errors.Is(err, model.ErrOpenAIHTTPStatus):
		return "MODEL_NOT_ALLOWED", "model_configuration"
	case errors.Is(err, tools.ErrToolOutputInvalid):
		return "TOOL_OUTPUT_INVALID", "model_output"
	case errors.Is(err, model.ErrClientNotConfigured), errors.Is(err, model.ErrOpenAIAPIKeyRequired), errors.Is(err, model.ErrSecretSourceFailed), errors.Is(err, model.ErrSecretNotFound), errors.Is(err, storage.ErrSecretNotFound), errors.Is(err, storage.ErrStrongholdUnavailable), errors.Is(err, storage.ErrSecretStoreAccessFailed):
		return "STRONGHOLD_ACCESS_FAILED", "model_credentials"
	default:
		return "", ""
	}
}

func executionFailureBubble(err error) string {
	switch {
	case errors.Is(err, execution.ErrRecoveryPointPrepareFailed):
		return "执行失败：执行前恢复点创建失败，请稍后重试。"
	case errors.Is(err, model.ErrClientNotConfigured), errors.Is(err, model.ErrOpenAIAPIKeyRequired), errors.Is(err, model.ErrSecretSourceFailed), errors.Is(err, model.ErrSecretNotFound), errors.Is(err, storage.ErrSecretNotFound), errors.Is(err, storage.ErrStrongholdUnavailable), errors.Is(err, storage.ErrSecretStoreAccessFailed):
		return "执行失败：当前模型凭证未配置或不可访问，请先完成模型设置后重试。"
	case errors.Is(err, model.ErrModelProviderRequired), errors.Is(err, model.ErrModelProviderUnsupported):
		return "执行失败：当前模型提供方未登记，请检查模型设置后重试。"
	case model.IsProviderRuntimeUnavailable(err):
		return "执行失败：当前模型服务暂时不可用，请稍后重试。"
	case errors.Is(err, model.ErrToolCallingNotSupported):
		return "执行失败：当前模型不支持所需的工具调用能力，请调整模型设置后重试。"
	case errors.Is(err, model.ErrOpenAIEndpointRequired), errors.Is(err, model.ErrOpenAIModelIDRequired), errors.Is(err, model.ErrOpenAIHTTPStatus):
		return "执行失败：当前模型配置不完整或请求被提供方拒绝，请检查模型设置后重试。"
	case errors.Is(err, tools.ErrToolOutputInvalid):
		return "执行失败：当前模型返回结果不完整，请稍后重试。"
	case errors.Is(err, tools.ErrWorkspaceBoundaryDenied):
		return "执行失败：目标超出工作区边界，已阻止本次操作。"
	case errors.Is(err, tools.ErrCommandNotAllowed):
		return "执行失败：命令存在高危风险，已被策略拦截。"
	case errors.Is(err, tools.ErrCapabilityDenied):
		return "执行失败：当前平台能力不可用，请检查环境后重试。"
	case errors.Is(err, tools.ErrToolExecutionFailed):
		return "执行失败：工具运行失败，请检查环境后重试。"
	default:
		return "执行失败：请稍后重试。"
	}
}

func (s *Service) buildExecutionAudit(task runengine.TaskRecord, toolCalls []tools.ToolCallRecord, deliveryResult map[string]any) ([]map[string]any, map[string]any) {
	if s.audit == nil {
		return nil, nil
	}

	auditRecords := make([]map[string]any, 0, len(toolCalls)+1)
	var tokenUsage map[string]any
	for _, toolCall := range toolCalls {
		auditRecord, usage, ok := s.audit.BuildToolAudit(task.TaskID, task.RunID, toolCall)
		if ok {
			auditRecords = append(auditRecords, auditRecord)
		}
		if len(usage) > 0 {
			tokenUsage = cloneMap(usage)
		}
	}
	if deliveryAudit := s.audit.BuildDeliveryAudit(task.TaskID, task.RunID, deliveryResult); len(deliveryAudit) > 0 {
		auditRecords = append(auditRecords, deliveryAudit)
	}

	return auditRecords, tokenUsage
}

func (s *Service) appendAuditData(task runengine.TaskRecord, auditRecords []map[string]any, tokenUsage map[string]any) runengine.TaskRecord {
	if len(auditRecords) == 0 && len(tokenUsage) == 0 {
		return task
	}
	updatedTask, ok := s.runEngine.AppendAuditData(task.TaskID, auditRecords, tokenUsage)
	if !ok {
		return task
	}
	return updatedTask
}

func (s *Service) buildBudgetDowngradeAudit(task runengine.TaskRecord, decision budgetDowngradeDecision) map[string]any {
	if !decision.Applied {
		return nil
	}
	return map[string]any{
		"audit_record_id": fmt.Sprintf("audit_budget_%s_%d", task.TaskID, time.Now().UnixNano()),
		"task_id":         task.TaskID,
		"run_id":          task.RunID,
		"category":        "budget_auto_downgrade",
		"action":          "budget_auto_downgrade.applied",
		"result":          "applied",
		"reason":          decision.TriggerReason,
		"created_at":      time.Now().Format(dateTimeLayout),
		"details": map[string]any{
			"trigger_stage":   decision.TriggerStage,
			"degrade_actions": append([]string(nil), decision.DegradeActions...),
			"summary":         decision.Summary,
			"trace":           cloneMap(decision.Trace),
		},
	}
}

func (s *Service) buildBudgetFailureAudit(task runengine.TaskRecord, executionErr error) map[string]any {
	if executionErr == nil {
		return nil
	}
	if failureCode, _ := classifyModelFailure(executionErr); failureCode == "" {
		return nil
	}
	return map[string]any{
		"audit_record_id": fmt.Sprintf("audit_budget_failure_%s_%d", task.TaskID, time.Now().UnixNano()),
		"task_id":         task.TaskID,
		"run_id":          task.RunID,
		"category":        "budget_auto_downgrade",
		"action":          "budget_auto_downgrade.failure_signal",
		"result":          "failed",
		"reason":          executionErr.Error(),
		"created_at":      time.Now().Format(dateTimeLayout),
	}
}

func (s *Service) recordBudgetDowngradeEvent(task runengine.TaskRecord, decision budgetDowngradeDecision) runengine.TaskRecord {
	if !decision.Applied {
		return task
	}
	s.publishRuntimeNotification(task.TaskID, "budget.downgrade.applied", map[string]any{
		"task_id":          task.TaskID,
		"run_id":           task.RunID,
		"trigger_reason":   decision.TriggerReason,
		"trigger_stage":    decision.TriggerStage,
		"degrade_actions":  append([]string(nil), decision.DegradeActions...),
		"summary":          decision.Summary,
		"trace":            cloneMap(decision.Trace),
		"budget_auto_down": true,
	})
	updatedTask, ok := s.runEngine.EmitRuntimeNotification(task.TaskID, "budget.downgrade.applied", map[string]any{
		"task_id":          task.TaskID,
		"run_id":           task.RunID,
		"trigger_reason":   decision.TriggerReason,
		"trigger_stage":    decision.TriggerStage,
		"degrade_actions":  append([]string(nil), decision.DegradeActions...),
		"summary":          decision.Summary,
		"trace":            cloneMap(decision.Trace),
		"budget_auto_down": true,
	})
	if !ok {
		return task
	}
	return updatedTask
}

// dateTimeLayout is the shared timestamp layout exposed by orchestrator RPC
// payloads.
const dateTimeLayout = time.RFC3339

func stringSliceValue(rawValue any) []string {
	values, ok := rawValue.([]string)
	if ok {
		return append([]string(nil), values...)
	}

	anyValues, ok := rawValue.([]any)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(anyValues))
	for _, rawItem := range anyValues {
		item, ok := rawItem.(string)
		if ok && strings.TrimSpace(item) != "" {
			result = append(result, item)
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}
