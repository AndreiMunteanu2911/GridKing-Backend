package profile

import (
	"context"
	"time"

	"GridKing-Backend/internal/bot"
	"GridKing-Backend/internal/game"
	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DailyPuzzle struct {
	ID          string      `firestore:"id" json:"id"`
	Date        string      `firestore:"date" json:"date"`
	State       game.State  `firestore:"state" json:"state"`
	LegalMoves  []game.Move `firestore:"-" json:"legal_moves"`
	BestMove    game.Move   `firestore:"best_move" json:"best_move,omitempty"`
	Difficulty  string      `firestore:"difficulty" json:"difficulty"`
	ScoreGap    int         `firestore:"score_gap" json:"-"`
	GeneratedAt time.Time   `firestore:"generated_at" json:"-"`
	Completed   bool        `firestore:"-" json:"completed"`
}

func (s *Store) DailyPuzzle(ctx context.Context, uid string) (DailyPuzzle, error) {
	today := time.Now().UTC()
	id := today.Format("2006-01-02")
	ref := s.client.Collection("daily_puzzles").Doc(id)
	doc, err := ref.Get(ctx)
	if status.Code(err) == codes.NotFound {
		generated, generateErr := bot.GenerateDailyPuzzle(today)
		if generateErr != nil {
			return DailyPuzzle{}, generateErr
		}
		value := DailyPuzzle{ID: id, Date: id, State: generated.State, BestMove: generated.BestMove, Difficulty: generated.Difficulty, ScoreGap: generated.ScoreGap, GeneratedAt: time.Now().UTC()}
		if _, createErr := ref.Create(ctx, value); createErr != nil && status.Code(createErr) != codes.AlreadyExists {
			return DailyPuzzle{}, createErr
		}
		doc, err = ref.Get(ctx)
	}
	if err != nil {
		return DailyPuzzle{}, err
	}
	var puzzle DailyPuzzle
	if err := doc.DataTo(&puzzle); err != nil {
		return DailyPuzzle{}, err
	}
	if _, completeErr := s.client.Collection("users").Doc(uid).Collection("puzzle_completions").Doc(id).Get(ctx); completeErr == nil {
		puzzle.Completed = true
	}
	return puzzle, nil
}

func (s *Store) CompletePuzzle(ctx context.Context, uid, puzzleID string, move game.Move) (bool, error) {
	puzzleDoc, err := s.client.Collection("daily_puzzles").Doc(puzzleID).Get(ctx)
	if err != nil {
		return false, err
	}
	var puzzle DailyPuzzle
	if err := puzzleDoc.DataTo(&puzzle); err != nil {
		return false, err
	}
	if !sameMove(move, puzzle.BestMove) {
		return false, nil
	}
	userRef := s.client.Collection("users").Doc(uid)
	completionRef := userRef.Collection("puzzle_completions").Doc(puzzleID)
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		userDoc, getErr := tx.Get(userRef)
		if getErr != nil {
			return getErr
		}
		if _, completeErr := tx.Get(completionRef); completeErr == nil {
			return nil
		} else if status.Code(completeErr) != codes.NotFound {
			return completeErr
		}
		var user Profile
		if err := userDoc.DataTo(&user); err != nil {
			return err
		}
		streak := 1
		puzzleDay, _ := time.Parse("2006-01-02", puzzleID)
		if previous, parseErr := time.Parse("2006-01-02", user.LastPuzzleDate); parseErr == nil {
			if previous.Format("2006-01-02") == puzzleDay.AddDate(0, 0, -1).Format("2006-01-02") {
				streak = user.PuzzleStreak + 1
			}
		}
		if err := tx.Set(completionRef, map[string]any{"completed_at": time.Now().UTC(), "move": move}); err != nil {
			return err
		}
		if err := tx.Update(userRef, []firestore.Update{{Path: "puzzle_streak", Value: streak}, {Path: "best_puzzle_streak", Value: max(user.BestPuzzleStreak, streak)}, {Path: "last_puzzle_date", Value: puzzleID}}); err != nil {
			return err
		}
		achievement := Achievement{ID: "first_puzzle", Title: "Problem Solver", Description: "Complete your first daily puzzle", UnlockedAt: time.Now().UTC()}
		if err := tx.Set(userRef.Collection("achievements").Doc(achievement.ID), achievement, firestore.MergeAll); err != nil {
			return err
		}
		if streak >= 3 {
			value := Achievement{ID: "puzzle_streak_3", Title: "Daily Thinker", Description: "Solve three daily puzzles in a row", UnlockedAt: time.Now().UTC()}
			if err := tx.Set(userRef.Collection("achievements").Doc(value.ID), value, firestore.MergeAll); err != nil {
				return err
			}
		}
		if streak >= 7 {
			value := Achievement{ID: "puzzle_streak_7", Title: "Puzzle Week", Description: "Solve seven daily puzzles in a row", UnlockedAt: time.Now().UTC()}
			if err := tx.Set(userRef.Collection("achievements").Doc(value.ID), value, firestore.MergeAll); err != nil {
				return err
			}
		}
		return nil
	})
	return err == nil, err
}

func sameMove(left, right game.Move) bool {
	if len(left.Path) != len(right.Path) {
		return false
	}
	for index := range left.Path {
		if left.Path[index] != right.Path[index] {
			return false
		}
	}
	return true
}
