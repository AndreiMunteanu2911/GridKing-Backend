package profile

import (
	"context"
	"errors"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Friend struct {
	Profile UserSummary `json:"profile"`
	Since   time.Time   `json:"since"`
	Online  bool        `json:"online"`
	InGame  bool        `json:"in_game"`
}

type UserSummary struct {
	UID             string `json:"uid"`
	Username        string `json:"username"`
	VisibleName     string `json:"visible_name"`
	MMR             int    `json:"mmr"`
	PresenceVisible bool   `json:"-"`
}

type FriendRequest struct {
	From      UserSummary `json:"from"`
	CreatedAt time.Time   `json:"created_at"`
}

type friendship struct {
	UID       string    `firestore:"uid"`
	CreatedAt time.Time `firestore:"created_at"`
}

type pendingRequest struct {
	FromUID   string    `firestore:"from_uid"`
	CreatedAt time.Time `firestore:"created_at"`
}

func summary(value Profile) UserSummary {
	return UserSummary{UID: value.UID, Username: value.Username, VisibleName: value.VisibleName, MMR: value.MMR, PresenceVisible: value.PresenceVisible}
}

func (s *Store) SearchUsers(ctx context.Context, uid, query string) ([]UserSummary, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if len(query) < 2 {
		return []UserSummary{}, nil
	}
	docs, err := s.client.Collection("users").OrderBy("username", firestore.Asc).StartAt(query).EndAt(query + "\uf8ff").Limit(12).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	result := make([]UserSummary, 0, len(docs))
	for _, doc := range docs {
		var value Profile
		if doc.DataTo(&value) == nil && value.UID != uid {
			result = append(result, summary(value))
		}
	}
	return result, nil
}

func (s *Store) SendFriendRequest(ctx context.Context, fromUID, toUID string) error {
	if fromUID == toUID || toUID == "" {
		return errors.New("choose another player")
	}
	fromRef := s.client.Collection("users").Doc(fromUID)
	toRef := s.client.Collection("users").Doc(toUID)
	friendRef := fromRef.Collection("friends").Doc(toUID)
	requestRef := toRef.Collection("friend_requests").Doc(fromUID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := tx.Get(toRef); err != nil {
			return errors.New("player not found")
		}
		if _, err := tx.Get(friendRef); err == nil {
			return errors.New("you are already friends")
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		if _, err := tx.Get(requestRef); err == nil {
			return errors.New("friend request already sent")
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		return tx.Create(requestRef, pendingRequest{FromUID: fromUID, CreatedAt: time.Now().UTC()})
	})
}

func (s *Store) FriendRequests(ctx context.Context, uid string) ([]FriendRequest, error) {
	docs, err := s.client.Collection("users").Doc(uid).Collection("friend_requests").OrderBy("created_at", firestore.Desc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	result := make([]FriendRequest, 0, len(docs))
	for _, doc := range docs {
		var request pendingRequest
		if doc.DataTo(&request) != nil {
			continue
		}
		player, getErr := s.Get(ctx, request.FromUID)
		if getErr == nil {
			result = append(result, FriendRequest{From: summary(player), CreatedAt: request.CreatedAt})
		}
	}
	return result, nil
}

func (s *Store) RespondFriendRequest(ctx context.Context, uid, fromUID string, accept bool) error {
	requestRef := s.client.Collection("users").Doc(uid).Collection("friend_requests").Doc(fromUID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := tx.Get(requestRef); err != nil {
			return errors.New("friend request not found")
		}
		if err := tx.Delete(requestRef); err != nil || !accept {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Set(s.client.Collection("users").Doc(uid).Collection("friends").Doc(fromUID), friendship{UID: fromUID, CreatedAt: now}); err != nil {
			return err
		}
		return tx.Set(s.client.Collection("users").Doc(fromUID).Collection("friends").Doc(uid), friendship{UID: uid, CreatedAt: now})
	})
}

func (s *Store) RemoveFriend(ctx context.Context, uid, friendUID string) error {
	batch := s.client.Batch()
	batch.Delete(s.client.Collection("users").Doc(uid).Collection("friends").Doc(friendUID))
	batch.Delete(s.client.Collection("users").Doc(friendUID).Collection("friends").Doc(uid))
	_, err := batch.Commit(ctx)
	return err
}

func (s *Store) Friends(ctx context.Context, uid string) ([]Friend, error) {
	docs, err := s.client.Collection("users").Doc(uid).Collection("friends").OrderBy("created_at", firestore.Asc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	result := make([]Friend, 0, len(docs))
	for _, doc := range docs {
		var relation friendship
		if doc.DataTo(&relation) != nil {
			continue
		}
		player, getErr := s.Get(ctx, relation.UID)
		if getErr == nil {
			result = append(result, Friend{Profile: summary(player), Since: relation.CreatedAt})
		}
	}
	return result, nil
}

func (s *Store) AreFriends(ctx context.Context, leftUID, rightUID string) bool {
	_, err := s.client.Collection("users").Doc(leftUID).Collection("friends").Doc(rightUID).Get(ctx)
	return err == nil
}

func (s *Store) SetPresenceVisibility(ctx context.Context, uid string, visible bool) error {
	_, err := s.client.Collection("users").Doc(uid).Update(ctx, []firestore.Update{{Path: "presence_visible", Value: visible}})
	return err
}
