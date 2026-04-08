package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const retrievalBackend = "sqlite_fts5+sqlite_vec"

var ErrStoreNotConfigured = errors.New("memory store not configured")
var ErrTaskIDRequired = errors.New("memory task_id is required")
var ErrRunIDRequired = errors.New("memory run_id is required")
var ErrSummaryRequired = errors.New("memory summary is required")
var ErrQueryRequired = errors.New("memory query is required")

type Service struct {
	store Store
}

func NewService(stores ...Store) *Service {
	var store Store
	if len(stores) > 0 {
		store = stores[0]
	}

	return &Service{store: store}
}

func NewInMemoryService() *Service {
	return NewService(NewInMemoryStore())
}

func (s *Service) RetrievalBackend() string {
	return retrievalBackend
}

func (s *Service) WriteSummary(ctx context.Context, summary MemorySummary) error {
	if err := validateSummary(summary); err != nil {
		return err
	}

	if strings.TrimSpace(summary.MemorySummaryID) == "" {
		summary.MemorySummaryID = fmt.Sprintf("memsum_%s_%s", summary.TaskID, summary.RunID)
	}

	if s.store == nil {
		return ErrStoreNotConfigured
	}

	return s.store.SaveSummary(ctx, summary)
}

func (s *Service) Search(ctx context.Context, query RetrievalQuery) ([]RetrievalHit, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}

	query = query.Normalized()

	if s.store == nil {
		return nil, ErrStoreNotConfigured
	}

	hits, err := s.store.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	return normalizeHits(hits, query.Limit), nil
}

func (s *Service) SearchMirrorReferences(ctx context.Context, query RetrievalQuery) ([]map[string]any, error) {
	references, err := s.SearchMirrorReferenceItems(ctx, query)
	if err != nil {
		return nil, err
	}

	return mirrorReferenceMaps(references), nil
}

func (s *Service) RecentReferences(ctx context.Context, limit int) ([]map[string]any, error) {
	references, err := s.RecentReferenceItems(ctx, limit)
	if err != nil {
		return nil, err
	}

	return mirrorReferenceMaps(references), nil
}

func (s *Service) SearchMirrorReferenceItems(ctx context.Context, query RetrievalQuery) ([]MirrorReference, error) {
	hits, err := s.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	return mirrorReferencesFromHits(hits), nil
}

func (s *Service) RecentReferenceItems(ctx context.Context, limit int) ([]MirrorReference, error) {
	if s.store == nil {
		return nil, ErrStoreNotConfigured
	}

	limit = normalizeRecentLimit(limit)
	summaries, err := s.store.ListSummaries(ctx, limit)
	if err != nil {
		return nil, err
	}

	return mirrorReferencesFromSummaries(summaries), nil
}

func validateSummary(summary MemorySummary) error {
	if strings.TrimSpace(summary.TaskID) == "" {
		return ErrTaskIDRequired
	}

	if strings.TrimSpace(summary.RunID) == "" {
		return ErrRunIDRequired
	}

	if strings.TrimSpace(summary.Summary) == "" {
		return ErrSummaryRequired
	}

	return nil
}

func validateQuery(query RetrievalQuery) error {
	if strings.TrimSpace(query.TaskID) == "" {
		return ErrTaskIDRequired
	}

	if strings.TrimSpace(query.RunID) == "" {
		return ErrRunIDRequired
	}

	if strings.TrimSpace(query.Query) == "" {
		return ErrQueryRequired
	}

	return nil
}

func normalizeHits(hits []RetrievalHit, limit int) []RetrievalHit {
	bestByMemoryID := make(map[string]RetrievalHit, len(hits))
	orderedKeys := make([]string, 0, len(hits))

	for _, hit := range hits {
		key := strings.TrimSpace(hit.MemoryID)
		if key == "" {
			key = strings.TrimSpace(hit.RetrievalHitID)
		}

		existing, ok := bestByMemoryID[key]
		if !ok {
			bestByMemoryID[key] = hit
			orderedKeys = append(orderedKeys, key)
			continue
		}

		if hit.Score > existing.Score {
			bestByMemoryID[key] = hit
		}
	}

	normalized := make([]RetrievalHit, 0, len(bestByMemoryID))
	for _, key := range orderedKeys {
		hit, ok := bestByMemoryID[key]
		if ok {
			normalized = append(normalized, hit)
			delete(bestByMemoryID, key)
		}
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Score == normalized[j].Score {
			return normalized[i].RetrievalHitID < normalized[j].RetrievalHitID
		}

		return normalized[i].Score > normalized[j].Score
	})

	if limit > 0 && len(normalized) > limit {
		return normalized[:limit]
	}

	return normalized
}

func normalizeRecentLimit(limit int) int {
	if limit <= 0 {
		return DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		return MaxSearchLimit
	}

	return limit
}

func mirrorReferencesFromHits(hits []RetrievalHit) []MirrorReference {
	if len(hits) == 0 {
		return []MirrorReference{}
	}

	result := make([]MirrorReference, 0, len(hits))
	for _, hit := range hits {
		reason := "当前任务命中了历史记忆"
		if strings.TrimSpace(hit.Source) != "" {
			reason = fmt.Sprintf("当前任务命中了来源为 %s 的历史记忆", hit.Source)
		}

		result = append(result, MirrorReference{MemoryID: hit.MemoryID, Reason: reason, Summary: hit.Summary})
	}

	return result
}

func mirrorReferencesFromSummaries(summaries []MemorySummary) []MirrorReference {
	if len(summaries) == 0 {
		return []MirrorReference{}
	}

	result := make([]MirrorReference, 0, len(summaries))
	for _, summary := range summaries {
		result = append(result, MirrorReference{
			MemoryID: summary.MemorySummaryID,
			Reason:   "最近写入的任务记忆摘要",
			Summary:  summary.Summary,
		})
	}

	return result
}

func mirrorReferenceMaps(references []MirrorReference) []map[string]any {
	if len(references) == 0 {
		return []map[string]any{}
	}

	result := make([]map[string]any, 0, len(references))
	for _, reference := range references {
		result = append(result, reference.Map())
	}

	return result
}
