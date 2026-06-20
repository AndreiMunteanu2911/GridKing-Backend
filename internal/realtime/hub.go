package realtime

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	"GridKing-Backend/internal/game"
	"GridKing-Backend/internal/profile"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type MatchStore interface {
	CompleteMatch(context.Context, profile.MatchRecord) error
}

type Hub struct {
	mu       sync.Mutex
	casual   []*Client
	ranked   []*Client
	matches  map[string]*Match
	store    MatchStore
	upgrader websocket.Upgrader
}

type Client struct {
	mu        sync.RWMutex
	uid       string
	profile   profile.Profile
	conn      *websocket.Conn
	send      chan any
	done      chan struct{}
	hub       *Hub
	match     *Match
	color     game.Color
	closeOnce sync.Once
}

type Match struct {
	mu        sync.Mutex
	id        string
	red       *Client
	black     *Client
	ranked    bool
	state     game.State
	createdAt time.Time
	finished  bool
}

type incoming struct {
	Type string    `json:"type"`
	Mode string    `json:"mode"`
	Move game.Move `json:"move"`
}

type event struct {
	Type       string           `json:"type"`
	MatchID    string           `json:"match_id,omitempty"`
	Color      game.Color       `json:"color,omitempty"`
	State      *game.State      `json:"state,omitempty"`
	LegalMoves []game.Move      `json:"legal_moves,omitempty"`
	Opponent   *profile.Profile `json:"opponent,omitempty"`
	Message    string           `json:"message,omitempty"`
}

func NewHub(store MatchStore) *Hub {
	allowedOrigin := os.Getenv("FRONTEND_ORIGIN")
	return &Hub{
		matches: make(map[string]*Match),
		store:   store,
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
			return allowedOrigin == "" || r.Header.Get("Origin") == allowedOrigin
		}},
	}
}

func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, user profile.Profile) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	client := &Client{uid: user.UID, profile: user, conn: conn, send: make(chan any, 16), done: make(chan struct{}), hub: h}
	go client.writeLoop()
	client.readLoop()
	return nil
}

func (c *Client) readLoop() {
	defer c.disconnect()
	c.conn.SetReadLimit(4096)
	c.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		return nil
	})
	for {
		var message incoming
		if err := c.conn.ReadJSON(&message); err != nil {
			return
		}
		switch message.Type {
		case "join_queue":
			c.hub.join(c, message.Mode)
		case "leave_queue":
			c.hub.leaveQueues(c)
		case "move":
			c.play(message.Move)
		case "resign":
			c.resign()
		default:
			c.push(event{Type: "error", Message: "unknown message type"})
		}
	}
}

func (c *Client) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case <-c.done:
			return
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok || c.conn.WriteJSON(message) != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}

func (h *Hub) join(client *Client, mode string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if client.currentMatch() != nil || contains(h.casual, client) || contains(h.ranked, client) {
		client.push(event{Type: "error", Message: "already queued or playing"})
		return
	}
	var queue *[]*Client
	ranked := false
	switch mode {
	case "casual":
		queue = &h.casual
	case "ranked":
		queue = &h.ranked
		ranked = true
	default:
		client.push(event{Type: "error", Message: "mode must be casual or ranked"})
		return
	}
	opponentIndex := -1
	for index, candidate := range *queue {
		if candidate.uid != client.uid {
			opponentIndex = index
			break
		}
	}
	if opponentIndex == -1 {
		*queue = append(*queue, client)
		client.push(event{Type: "queued", Message: mode})
		return
	}
	opponent := (*queue)[opponentIndex]
	*queue = append((*queue)[:opponentIndex], (*queue)[opponentIndex+1:]...)
	match := &Match{id: uuid.NewString(), red: opponent, black: client, ranked: ranked, state: game.NewState(), createdAt: time.Now().UTC()}
	opponent.setMatch(match, game.Red)
	client.setMatch(match, game.Black)
	h.matches[match.id] = match
	state := match.state
	opponent.push(event{Type: "match_found", MatchID: match.id, Color: game.Red, State: &state, LegalMoves: state.LegalMoves(), Opponent: publicProfile(client.profile)})
	client.push(event{Type: "match_found", MatchID: match.id, Color: game.Black, State: &state, LegalMoves: state.LegalMoves(), Opponent: publicProfile(opponent.profile)})
}

func (c *Client) play(move game.Move) {
	match, color := c.matchDetails()
	if match == nil {
		c.push(event{Type: "error", Message: "not in a match"})
		return
	}
	match.mu.Lock()
	defer match.mu.Unlock()
	if match.finished || match.state.Turn != color {
		c.push(event{Type: "error", Message: "it is not your turn"})
		return
	}
	next, err := match.state.Apply(move)
	if err != nil {
		c.push(event{Type: "error", Message: err.Error()})
		return
	}
	match.state = next
	match.broadcastLocked("state")
	if next.Winner != 0 {
		match.finishLocked(next.Winner, next.Reason)
	}
}

func (c *Client) resign() {
	match, color := c.matchDetails()
	if match == nil {
		return
	}
	match.mu.Lock()
	defer match.mu.Unlock()
	if !match.finished {
		winner := game.Red
		if color == game.Red {
			winner = game.Black
		}
		match.state = match.state.WithWinner(winner, "resignation")
		match.broadcastLocked("state")
		match.finishLocked(winner, "resignation")
	}
}

func (c *Client) disconnect() {
	c.closeOnce.Do(func() {
		c.hub.leaveQueues(c)
		match, color := c.matchDetails()
		if match != nil {
			match.mu.Lock()
			if !match.finished {
				winner := game.Red
				if color == game.Red {
					winner = game.Black
				}
				match.state = match.state.WithWinner(winner, "opponent_disconnected")
				match.broadcastLocked("opponent_disconnected")
				match.finishLocked(winner, "opponent_disconnected")
			}
			match.mu.Unlock()
		}
		close(c.done)
	})
}

func (m *Match) broadcastLocked(kind string) {
	state := m.state
	payload := event{Type: kind, MatchID: m.id, State: &state, LegalMoves: state.LegalMoves()}
	m.red.push(payload)
	m.black.push(payload)
}

func (m *Match) finishLocked(winner game.Color, reason string) {
	if m.finished {
		return
	}
	m.finished = true
	hub := m.red.hub
	hub.mu.Lock()
	delete(hub.matches, m.id)
	hub.mu.Unlock()
	state := m.state
	gameOver := event{Type: "game_over", MatchID: m.id, State: &state, Message: reason}
	m.red.push(gameOver)
	m.black.push(gameOver)
	winnerUID := m.red.uid
	if winner == game.Black {
		winnerUID = m.black.uid
	}
	record := profile.MatchRecord{ID: m.id, RedUID: m.red.uid, BlackUID: m.black.uid, WinnerUID: winnerUID, Ranked: m.ranked, Reason: reason, CreatedAt: m.createdAt, EndedAt: time.Now().UTC()}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.red.hub.store.CompleteMatch(ctx, record)
	}()
}

func (h *Hub) leaveQueues(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.casual = remove(h.casual, client)
	h.ranked = remove(h.ranked, client)
}

func (c *Client) push(value any) {
	select {
	case <-c.done:
		return
	case c.send <- value:
	default:
		c.conn.Close()
	}
}

func (c *Client) currentMatch() *Match {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.match
}

func (c *Client) matchDetails() (*Match, game.Color) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.match, c.color
}

func (c *Client) setMatch(match *Match, color game.Color) {
	c.mu.Lock()
	c.match, c.color = match, color
	c.mu.Unlock()
}

func publicProfile(value profile.Profile) *profile.Profile {
	return &profile.Profile{UID: value.UID, Username: value.Username, VisibleName: value.VisibleName, MMR: value.MMR, MatchesPlayed: value.MatchesPlayed, Wins: value.Wins}
}

func contains(values []*Client, target *Client) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func remove(values []*Client, target *Client) []*Client {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
