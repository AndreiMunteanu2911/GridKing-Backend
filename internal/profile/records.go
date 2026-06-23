package profile

import (
	"context"
	"errors"
	"sort"

	"GridKing-Backend/internal/game"
	"cloud.google.com/go/firestore"
)

func (s *Store) GetMatch(ctx context.Context, uid, id string) (MatchRecord, error) {
	doc, err := s.client.Collection("matches").Doc(id).Get(ctx)
	if err != nil {
		return MatchRecord{}, err
	}
	var record MatchRecord
	if err := doc.DataTo(&record); err != nil {
		return MatchRecord{}, err
	}
	if record.RedUID != uid && record.BlackUID != uid {
		return MatchRecord{}, errors.New("match not found")
	}
	normalizeMatch(&record)
	return record, nil
}

func (s *Store) ListMatches(ctx context.Context, uid string, limit int) ([]MatchRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	redDocs, err := s.client.Collection("matches").Where("red_uid", "==", uid).Limit(limit).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	blackDocs, err := s.client.Collection("matches").Where("black_uid", "==", uid).Limit(limit).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]MatchRecord)
	for _, doc := range append(redDocs, blackDocs...) {
		var record MatchRecord
		if doc.DataTo(&record) == nil {
			normalizeMatch(&record)
			byID[record.ID] = record
		}
	}
	result := make([]MatchRecord, 0, len(byID))
	for _, record := range byID {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EndedAt.After(result[j].EndedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func normalizeMatch(record *MatchRecord) {
	if record.Moves == nil {
		record.Moves = []MatchMove{}
	}
	if record.Analysis == nil {
		record.Analysis = []game.MoveAnalysis{}
	}
	if record.Mode == "" {
		record.Mode = "pvp"
	}
}

func (s *Store) SaveAnalysis(ctx context.Context, matchID string, analysis []game.MoveAnalysis) error {
	_, err := s.client.Collection("matches").Doc(matchID).Update(ctx, []firestore.Update{{Path: "analysis", Value: analysis}, {Path: "analysis_status", Value: "complete"}})
	return err
}

func (s *Store) SaveBotMatch(ctx context.Context, record MatchRecord) error {
	batch := s.client.Batch()
	batch.Create(s.client.Collection("matches").Doc(record.ID), record)
	if record.WinnerUID != "" && record.WinnerUID != "bot" {
		achievement := Achievement{ID: "bot_win", Title: "Machine Breaker", Description: "Defeat a bot", UnlockedAt: record.EndedAt}
		batch.Set(s.client.Collection("users").Doc(record.WinnerUID).Collection("achievements").Doc(achievement.ID), achievement, firestore.MergeAll)
	}
	_, err := batch.Commit(ctx)
	return err
}

func (s *Store) Achievements(ctx context.Context, uid string) ([]Achievement, error) {
	docs, err := s.client.Collection("users").Doc(uid).Collection("achievements").OrderBy("unlocked_at", firestore.Desc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	result := make([]Achievement, 0, len(docs))
	for _, doc := range docs {
		var value Achievement
		if doc.DataTo(&value) == nil {
			result = append(result, value)
		}
	}
	return result, nil
}
