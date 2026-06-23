package profile

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"GridKing-Backend/internal/game"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Profile struct {
	UID              string `firestore:"uid" json:"uid"`
	Username         string `firestore:"username" json:"username"`
	VisibleName      string `firestore:"visible_name" json:"visible_name"`
	MMR              int    `firestore:"mmr" json:"mmr"`
	MatchesPlayed    int    `firestore:"matches_played" json:"matches_played"`
	Wins             int    `firestore:"wins" json:"wins"`
	WinStreak        int    `firestore:"win_streak" json:"win_streak"`
	BestWinStreak    int    `firestore:"best_win_streak" json:"best_win_streak"`
	PuzzleStreak     int    `firestore:"puzzle_streak" json:"puzzle_streak"`
	BestPuzzleStreak int    `firestore:"best_puzzle_streak" json:"best_puzzle_streak"`
	LastPuzzleDate   string `firestore:"last_puzzle_date" json:"-"`
	PresenceVisible  bool   `firestore:"presence_visible" json:"presence_visible"`
}

type MatchMove struct {
	Ply            int        `firestore:"ply" json:"ply"`
	Color          game.Color `firestore:"color" json:"color"`
	Move           game.Move  `firestore:"move" json:"move"`
	CapturedPieces []int      `firestore:"captured_pieces" json:"captured_pieces"`
	State          game.State `firestore:"state" json:"state"`
	PlayedAt       time.Time  `firestore:"played_at" json:"played_at"`
}

type MatchRecord struct {
	ID               string              `firestore:"id" json:"id"`
	Mode             string              `firestore:"mode" json:"mode"`
	RedUID           string              `firestore:"red_uid" json:"red_uid"`
	BlackUID         string              `firestore:"black_uid" json:"black_uid"`
	RedName          string              `firestore:"red_name" json:"red_name"`
	BlackName        string              `firestore:"black_name" json:"black_name"`
	WinnerUID        string              `firestore:"winner_uid" json:"winner_uid"`
	Ranked           bool                `firestore:"ranked" json:"ranked"`
	Invited          bool                `firestore:"invited" json:"invited"`
	Reason           string              `firestore:"reason" json:"reason"`
	InitialState     game.State          `firestore:"initial_state" json:"initial_state"`
	Moves            []MatchMove         `firestore:"moves" json:"moves"`
	Analysis         []game.MoveAnalysis `firestore:"analysis,omitempty" json:"analysis,omitempty"`
	AnalysisStatus   string              `firestore:"analysis_status" json:"analysis_status"`
	RedRemainingMS   int64               `firestore:"red_remaining_ms" json:"red_remaining_ms"`
	BlackRemainingMS int64               `firestore:"black_remaining_ms" json:"black_remaining_ms"`
	BotDifficulty    string              `firestore:"bot_difficulty,omitempty" json:"bot_difficulty,omitempty"`
	BotPersonality   string              `firestore:"bot_personality,omitempty" json:"bot_personality,omitempty"`
	CreatedAt        time.Time           `firestore:"created_at" json:"created_at"`
	EndedAt          time.Time           `firestore:"ended_at" json:"ended_at"`
}

type Achievement struct {
	ID          string    `firestore:"id" json:"id"`
	Title       string    `firestore:"title" json:"title"`
	Description string    `firestore:"description" json:"description"`
	UnlockedAt  time.Time `firestore:"unlocked_at" json:"unlocked_at"`
}

type Store struct{ client *firestore.Client }

func NewStore(client *firestore.Client) *Store { return &Store{client: client} }

func (s *Store) Get(ctx context.Context, uid string) (Profile, error) {
	snapshot, err := s.client.Collection("users").Doc(uid).Get(ctx)
	if err != nil {
		return Profile{}, err
	}
	var result Profile
	err = snapshot.DataTo(&result)
	if _, exists := snapshot.Data()["presence_visible"]; !exists {
		result.PresenceVisible = true
	}
	return result, err
}

func (s *Store) Create(ctx context.Context, value Profile) (Profile, error) {
	value.Username = strings.ToLower(strings.TrimSpace(value.Username))
	value.VisibleName = strings.TrimSpace(value.VisibleName)
	if len(value.Username) < 3 || len(value.Username) > 20 || len(value.VisibleName) < 2 || len(value.VisibleName) > 30 {
		return Profile{}, errors.New("username or visible name has an invalid length")
	}
	for _, character := range value.Username {
		if !(character == '_' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return Profile{}, errors.New("username may contain lowercase letters, numbers, and underscores")
		}
	}
	value.MMR = 1200
	value.MatchesPlayed = 0
	value.Wins = 0
	value.PresenceVisible = true
	userRef := s.client.Collection("users").Doc(value.UID)
	nameRef := s.client.Collection("usernames").Doc(value.Username)
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := tx.Get(nameRef); err == nil {
			return errors.New("username is already taken")
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		if _, err := tx.Get(userRef); err == nil {
			return errors.New("profile already exists")
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		if err := tx.Create(nameRef, map[string]any{"uid": value.UID}); err != nil {
			return err
		}
		return tx.Create(userRef, value)
	})
	return value, err
}

func (s *Store) Leaderboard(ctx context.Context, limit int) ([]Profile, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	docs, err := s.client.Collection("users").OrderBy("mmr", firestore.Desc).Limit(limit).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	result := make([]Profile, 0, len(docs))
	for _, doc := range docs {
		var value Profile
		if doc.DataTo(&value) == nil {
			result = append(result, value)
		}
	}
	return result, nil
}

func (s *Store) CompleteMatch(ctx context.Context, record MatchRecord) error {
	redRef := s.client.Collection("users").Doc(record.RedUID)
	blackRef := s.client.Collection("users").Doc(record.BlackUID)
	matchRef := s.client.Collection("matches").Doc(record.ID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		redDoc, err := tx.Get(redRef)
		if err != nil {
			return err
		}
		blackDoc, err := tx.Get(blackRef)
		if err != nil {
			return err
		}
		var red, black Profile
		if err := redDoc.DataTo(&red); err != nil {
			return err
		}
		if err := blackDoc.DataTo(&black); err != nil {
			return err
		}
		redScore := 0.5
		if record.WinnerUID == red.UID {
			redScore = 1
		} else if record.WinnerUID == black.UID {
			redScore = 0
		}
		ratingAllowed := record.Ranked
		var ratingLedger *firestore.DocumentRef
		if record.Ranked && record.Invited {
			pair := record.RedUID + "_" + record.BlackUID
			if record.BlackUID < record.RedUID {
				pair = record.BlackUID + "_" + record.RedUID
			}
			ratingLedger = s.client.Collection("rated_friend_pairs").Doc(time.Now().UTC().Format("2006-01-02") + "_" + pair)
			if _, ledgerErr := tx.Get(ratingLedger); ledgerErr == nil {
				ratingAllowed = false
			} else if status.Code(ledgerErr) != codes.NotFound {
				return ledgerErr
			}
		}
		redUpdate := []firestore.Update{{Path: "matches_played", Value: firestore.Increment(1)}, {Path: "win_streak", Value: 0}}
		blackUpdate := []firestore.Update{{Path: "matches_played", Value: firestore.Increment(1)}, {Path: "win_streak", Value: 0}}
		if record.WinnerUID == red.UID {
			redStreak := red.WinStreak + 1
			redUpdate = append(redUpdate, firestore.Update{Path: "wins", Value: firestore.Increment(1)}, firestore.Update{Path: "win_streak", Value: redStreak}, firestore.Update{Path: "best_win_streak", Value: max(red.BestWinStreak, redStreak)})
		} else if record.WinnerUID == black.UID {
			blackStreak := black.WinStreak + 1
			blackUpdate = append(blackUpdate, firestore.Update{Path: "wins", Value: firestore.Increment(1)}, firestore.Update{Path: "win_streak", Value: blackStreak}, firestore.Update{Path: "best_win_streak", Value: max(black.BestWinStreak, blackStreak)})
		}
		if ratingAllowed {
			expectedRed := 1 / (1 + math.Pow(10, float64(black.MMR-red.MMR)/400))
			change := int(math.Round(32 * (redScore - expectedRed)))
			redUpdate = append(redUpdate, firestore.Update{Path: "mmr", Value: max(100, red.MMR+change)})
			blackUpdate = append(blackUpdate, firestore.Update{Path: "mmr", Value: max(100, black.MMR-change)})
		}
		if err := tx.Update(redRef, redUpdate); err != nil {
			return err
		}
		if err := tx.Update(blackRef, blackUpdate); err != nil {
			return err
		}
		if ratingLedger != nil && ratingAllowed {
			if err := tx.Create(ratingLedger, map[string]any{"match_id": record.ID, "created_at": time.Now().UTC()}); err != nil {
				return err
			}
		}
		if err := tx.Create(matchRef, record); err != nil {
			return err
		}
		award := func(uid, id, title, description string) {
			ref := s.client.Collection("users").Doc(uid).Collection("achievements").Doc(id)
			tx.Set(ref, Achievement{ID: id, Title: title, Description: description, UnlockedAt: time.Now().UTC()}, firestore.MergeAll)
		}
		if red.MatchesPlayed == 0 {
			award(red.UID, "first_game", "First Move", "Complete your first PvP game")
		}
		if black.MatchesPlayed == 0 {
			award(black.UID, "first_game", "First Move", "Complete your first PvP game")
		}
		if record.WinnerUID != "" {
			award(record.WinnerUID, "first_win", "First Victory", "Win your first PvP game")
		}
		if red.MatchesPlayed+1 >= 10 {
			award(red.UID, "games_10", "Regular", "Complete ten PvP games")
		}
		if black.MatchesPlayed+1 >= 10 {
			award(black.UID, "games_10", "Regular", "Complete ten PvP games")
		}
		if record.WinnerUID == red.UID && red.Wins+1 >= 10 {
			award(red.UID, "wins_10", "Proven Winner", "Win ten PvP games")
		}
		if record.WinnerUID == black.UID && black.Wins+1 >= 10 {
			award(black.UID, "wins_10", "Proven Winner", "Win ten PvP games")
		}
		if record.WinnerUID == red.UID && red.WinStreak+1 >= 3 {
			award(red.UID, "streak_3", "On a Roll", "Win three PvP games in a row")
		}
		if record.WinnerUID == black.UID && black.WinStreak+1 >= 3 {
			award(black.UID, "streak_3", "On a Roll", "Win three PvP games in a row")
		}
		if record.WinnerUID == red.UID && red.WinStreak+1 >= 5 {
			award(red.UID, "streak_5", "Unstoppable", "Win five PvP games in a row")
		}
		if record.WinnerUID == black.UID && black.WinStreak+1 >= 5 {
			award(black.UID, "streak_5", "Unstoppable", "Win five PvP games in a row")
		}
		return nil
	})
}
