package profile

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Profile struct {
	UID           string `firestore:"uid" json:"uid"`
	Username      string `firestore:"username" json:"username"`
	VisibleName   string `firestore:"visible_name" json:"visible_name"`
	MMR           int    `firestore:"mmr" json:"mmr"`
	MatchesPlayed int    `firestore:"matches_played" json:"matches_played"`
	Wins          int    `firestore:"wins" json:"wins"`
}

type MatchRecord struct {
	ID        string    `firestore:"id"`
	RedUID    string    `firestore:"red_uid"`
	BlackUID  string    `firestore:"black_uid"`
	WinnerUID string    `firestore:"winner_uid"`
	Ranked    bool      `firestore:"ranked"`
	Reason    string    `firestore:"reason"`
	CreatedAt time.Time `firestore:"created_at"`
	EndedAt   time.Time `firestore:"ended_at"`
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
		redUpdate := []firestore.Update{{Path: "matches_played", Value: firestore.Increment(1)}}
		blackUpdate := []firestore.Update{{Path: "matches_played", Value: firestore.Increment(1)}}
		if record.WinnerUID == red.UID {
			redUpdate = append(redUpdate, firestore.Update{Path: "wins", Value: firestore.Increment(1)})
		} else if record.WinnerUID == black.UID {
			blackUpdate = append(blackUpdate, firestore.Update{Path: "wins", Value: firestore.Increment(1)})
		}
		if record.Ranked {
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
		return tx.Create(matchRef, record)
	})
}
