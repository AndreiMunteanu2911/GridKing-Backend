package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type IdentityService struct {
	apiKey string
	client *http.Client
}

type Session struct {
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    string `json:"expires_in"`
	UID          string `json:"uid"`
	Email        string `json:"email,omitempty"`
}

func NewIdentityService(apiKey string) *IdentityService {
	return &IdentityService{apiKey: apiKey, client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *IdentityService) SignIn(ctx context.Context, email, password string) (Session, error) {
	payload := map[string]any{"email": email, "password": password, "returnSecureToken": true}
	var response struct {
		IDToken      string `json:"idToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    string `json:"expiresIn"`
		LocalID      string `json:"localId"`
		Email        string `json:"email"`
	}
	if err := s.postJSON(ctx, "https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key="+url.QueryEscape(s.apiKey), payload, &response); err != nil {
		return Session{}, err
	}
	return Session{IDToken: response.IDToken, RefreshToken: response.RefreshToken, ExpiresIn: response.ExpiresIn, UID: response.LocalID, Email: response.Email}, nil
}

func (s *IdentityService) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://securetoken.googleapis.com/v1/token?key="+url.QueryEscape(s.apiKey), bytes.NewBufferString(form.Encode()))
	if err != nil {
		return Session{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    string `json:"expires_in"`
		UserID       string `json:"user_id"`
	}
	if err := s.do(request, &response); err != nil {
		return Session{}, err
	}
	return Session{IDToken: response.IDToken, RefreshToken: response.RefreshToken, ExpiresIn: response.ExpiresIn, UID: response.UserID}, nil
}

func (s *IdentityService) postJSON(ctx context.Context, endpoint string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return s.do(request, target)
}

func (s *IdentityService) do(request *http.Request, target any) error {
	if s.apiKey == "" {
		return errors.New("firebase web API key is not configured")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var firebaseError struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &firebaseError) == nil && firebaseError.Error.Message != "" {
			return fmt.Errorf("firebase authentication failed: %s", firebaseError.Error.Message)
		}
		return fmt.Errorf("firebase authentication failed with status %d", response.StatusCode)
	}
	return json.Unmarshal(body, target)
}
