package realtime

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"GridKing-Backend/internal/bot"
	"GridKing-Backend/internal/game"
	"GridKing-Backend/internal/profile"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	matchTime       = 10 * time.Minute
	reconnectWindow = 30 * time.Second
	inviteLifetime  = 60 * time.Second
)

type MatchStore interface {
	CompleteMatch(context.Context, profile.MatchRecord) error
	SaveAnalysis(context.Context, string, []game.MoveAnalysis) error
	AreFriends(context.Context, string, string) bool
}

type Hub struct {
	mu       sync.Mutex
	casual   []*Client
	ranked   []*Client
	matches  map[string]*Match
	clients  map[string]*Client
	detached map[string]*Client
	invites  map[string]*Invite
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
	mu              sync.Mutex
	id              string
	red             *Client
	black           *Client
	ranked          bool
	invited         bool
	state           game.State
	initial         game.State
	history         []profile.MatchMove
	createdAt       time.Time
	finished        bool
	redRemaining    time.Duration
	blackRemaining  time.Duration
	turnStartedAt   time.Time
	clockTimer      *time.Timer
	reconnectTimers map[game.Color]*time.Timer
	rematchVotes    map[string]bool
}

type Invite struct {
	ID        string
	From      *Client
	To        *Client
	Ranked    bool
	CreatedAt time.Time
	Timer     *time.Timer
}

type incoming struct {
	Type      string    `json:"type"`
	Mode      string    `json:"mode"`
	Move      game.Move `json:"move"`
	FriendUID string    `json:"friend_uid"`
	Ranked    bool      `json:"ranked"`
	InviteID  string    `json:"invite_id"`
	Accept    bool      `json:"accept"`
}

type event struct {
	Type           string              `json:"type"`
	MatchID        string              `json:"match_id,omitempty"`
	Color          game.Color          `json:"color,omitempty"`
	State          *game.State         `json:"state,omitempty"`
	LegalMoves     []game.Move         `json:"legal_moves,omitempty"`
	History        []profile.MatchMove `json:"history,omitempty"`
	Opponent       *profile.Profile    `json:"opponent,omitempty"`
	Message        string              `json:"message,omitempty"`
	Ranked         bool                `json:"ranked,omitempty"`
	Invited        bool                `json:"invited,omitempty"`
	RedTimeMS      int64               `json:"red_time_ms"`
	BlackTimeMS    int64               `json:"black_time_ms"`
	TurnDeadline   time.Time           `json:"turn_deadline,omitempty"`
	ServerTime     time.Time           `json:"server_time,omitempty"`
	InviteID       string              `json:"invite_id,omitempty"`
	Inviter        *profile.Profile    `json:"inviter,omitempty"`
	ReconnectUntil time.Time           `json:"reconnect_until,omitempty"`
}

func NewHub(store MatchStore) *Hub {
	allowedOrigin := os.Getenv("FRONTEND_ORIGIN")
	origins := strings.Split(allowedOrigin, ",")
	for i := range origins {
		origins[i] = strings.TrimRight(strings.TrimSpace(origins[i]), "/")
	}
	return &Hub{
		matches: make(map[string]*Match), clients: make(map[string]*Client), detached: make(map[string]*Client), invites: make(map[string]*Invite), store: store,
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
			if allowedOrigin == "" {
				return true
			}
			requestOrigin := r.Header.Get("Origin")
			for _, origin := range origins {
				if origin == requestOrigin {
					return true
				}
			}
			return false
		}},
	}
}

func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, user profile.Profile) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	client := &Client{uid: user.UID, profile: user, conn: conn, send: make(chan any, 32), done: make(chan struct{}), hub: h}
	old := h.register(client)
	if old != nil {
		h.reattach(old, client)
		_ = old.conn.Close()
	}
	go client.writeLoop()
	client.readLoop()
	return nil
}

func (h *Hub) register(client *Client) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.clients[client.uid]
	if old == nil {
		old = h.detached[client.uid]
	}
	delete(h.detached, client.uid)
	h.clients[client.uid] = client
	return old
}

func (h *Hub) reattach(old, replacement *Client) {
	match, color := old.matchDetails()
	if match == nil {
		return
	}
	replacement.setMatch(match, color)
	match.mu.Lock()
	if color == game.Red && match.red == old {
		match.red = replacement
	} else if color == game.Black && match.black == old {
		match.black = replacement
	} else {
		match.mu.Unlock()
		return
	}
	if timer := match.reconnectTimers[color]; timer != nil {
		timer.Stop()
		delete(match.reconnectTimers, color)
	}
	if match.finished {
		replacement.push(match.eventLocked("game_over", color))
	} else {
		replacement.push(match.eventLocked("match_found", color))
		match.opponent(color).push(event{Type: "opponent_reconnected", Message: "Opponent reconnected"})
	}
	match.mu.Unlock()
}

func (c *Client) readLoop() {
	defer c.disconnect()
	c.conn.SetReadLimit(8192)
	_ = c.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })
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
		case "rematch":
			c.requestRematch()
		case "leave_match":
			c.leaveFinishedMatch()
		case "invite_friend":
			c.hub.inviteFriend(c, message.FriendUID, message.Ranked)
		case "respond_invite":
			c.hub.respondInvite(c, message.InviteID, message.Accept)
		default:
			c.push(event{Type: "error", Message: "unknown message type"})
		}
	}
}

func (c *Client) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); _ = c.conn.Close() }()
	for {
		select {
		case <-c.done:
			return
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok || c.conn.WriteJSON(message) != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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
	for index, candidate := range *queue {
		if candidate.uid == client.uid {
			continue
		}
		*queue = append((*queue)[:index], (*queue)[index+1:]...)
		h.startMatchLocked(candidate, client, ranked, false)
		return
	}
	*queue = append(*queue, client)
	client.push(event{Type: "queued", Message: mode})
}

func (h *Hub) startMatchLocked(red, black *Client, ranked, invited bool) *Match {
	now := time.Now().UTC()
	initial := game.NewState()
	match := &Match{id: uuid.NewString(), red: red, black: black, ranked: ranked, invited: invited, state: initial, initial: initial, createdAt: now, redRemaining: matchTime, blackRemaining: matchTime, turnStartedAt: now, reconnectTimers: make(map[game.Color]*time.Timer), rematchVotes: make(map[string]bool)}
	red.setMatch(match, game.Red)
	black.setMatch(match, game.Black)
	h.matches[match.id] = match
	match.scheduleClockLocked()
	red.push(match.eventLocked("match_found", game.Red))
	black.push(match.eventLocked("match_found", game.Black))
	return match
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
	if match.settleClockLocked(time.Now().UTC()) {
		match.timeoutLocked(color)
		return
	}
	before := match.state
	next, err := before.Apply(move)
	if err != nil {
		c.push(event{Type: "error", Message: err.Error()})
		return
	}
	match.state = next
	match.history = append(match.history, profile.MatchMove{Ply: len(match.history) + 1, Color: before.Turn, Move: move, CapturedPieces: before.CapturedPieces(move), State: next, PlayedAt: time.Now().UTC()})
	match.turnStartedAt = time.Now().UTC()
	match.scheduleClockLocked()
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
	if match.finished {
		return
	}
	_ = match.settleClockLocked(time.Now().UTC())
	winner := game.Red
	if color == game.Red {
		winner = game.Black
	}
	match.state = match.state.WithWinner(winner, "resignation")
	match.broadcastLocked("state")
	match.finishLocked(winner, "resignation")
}

func (c *Client) requestRematch() {
	match, _ := c.matchDetails()
	if match == nil {
		return
	}
	match.mu.Lock()
	if !match.finished {
		match.mu.Unlock()
		return
	}
	match.rematchVotes[c.uid] = true
	other := match.black
	if c.uid == match.black.uid {
		other = match.red
	}
	if !match.rematchVotes[other.uid] {
		other.push(event{Type: "rematch_requested", MatchID: match.id, Message: "Opponent wants a rematch"})
		c.push(event{Type: "rematch_waiting", MatchID: match.id})
		match.mu.Unlock()
		return
	}
	red, black, ranked, invited, oldID := match.black, match.red, match.ranked, match.invited, match.id
	match.mu.Unlock()
	c.hub.mu.Lock()
	delete(c.hub.matches, oldID)
	c.hub.startMatchLocked(red, black, ranked, invited)
	c.hub.mu.Unlock()
}

func (c *Client) leaveFinishedMatch() {
	match, color := c.matchDetails()
	if match == nil {
		return
	}
	match.mu.Lock()
	if match.finished {
		delete(match.rematchVotes, c.uid)
		other := match.opponent(color)
		if match.rematchVotes[other.uid] {
			other.push(event{Type: "rematch_declined", MatchID: match.id, Message: "Opponent declined the rematch"})
			delete(match.rematchVotes, other.uid)
		}
		match.mu.Unlock()
		c.clearMatch(match)
		return
	}
	match.mu.Unlock()
}

func (m *Match) settleClockLocked(now time.Time) bool {
	if m.finished || m.turnStartedAt.IsZero() {
		return false
	}
	elapsed := now.Sub(m.turnStartedAt)
	if m.state.Turn == game.Red {
		m.redRemaining -= elapsed
		m.turnStartedAt = now
		return m.redRemaining <= 0
	}
	m.blackRemaining -= elapsed
	m.turnStartedAt = now
	return m.blackRemaining <= 0
}

func (m *Match) scheduleClockLocked() {
	if m.clockTimer != nil {
		m.clockTimer.Stop()
	}
	remaining := m.redRemaining
	turn := m.state.Turn
	if turn == game.Black {
		remaining = m.blackRemaining
	}
	started := m.turnStartedAt
	if remaining < 0 {
		remaining = 0
	}
	m.clockTimer = time.AfterFunc(remaining, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.finished || m.state.Turn != turn || !m.turnStartedAt.Equal(started) {
			return
		}
		if m.settleClockLocked(time.Now().UTC()) {
			m.timeoutLocked(turn)
		}
	})
}

func (m *Match) timeoutLocked(loser game.Color) {
	winner := game.Red
	if loser == game.Red {
		winner = game.Black
	}
	m.state = m.state.WithWinner(winner, "timeout")
	m.broadcastLocked("state")
	m.finishLocked(winner, "timeout")
}

func (m *Match) eventLocked(kind string, recipient game.Color) event {
	now := time.Now().UTC()
	redTime, blackTime := m.clockValuesLocked(now)
	deadline := now.Add(redTime)
	if m.state.Turn == game.Black {
		deadline = now.Add(blackTime)
	}
	state := m.state
	return event{Type: kind, MatchID: m.id, Color: recipient, State: &state, LegalMoves: state.LegalMoves(), History: append([]profile.MatchMove(nil), m.history...), Opponent: publicProfile(m.opponent(recipient).profile), Ranked: m.ranked, Invited: m.invited, RedTimeMS: redTime.Milliseconds(), BlackTimeMS: blackTime.Milliseconds(), TurnDeadline: deadline, ServerTime: now}
}

func (m *Match) clockValuesLocked(now time.Time) (time.Duration, time.Duration) {
	redTime, blackTime := m.redRemaining, m.blackRemaining
	if !m.finished {
		if m.state.Turn == game.Red {
			redTime -= now.Sub(m.turnStartedAt)
		} else {
			blackTime -= now.Sub(m.turnStartedAt)
		}
	}
	return max(0, redTime), max(0, blackTime)
}

func (m *Match) broadcastLocked(kind string) {
	m.red.push(m.eventLocked(kind, game.Red))
	m.black.push(m.eventLocked(kind, game.Black))
}

func (m *Match) finishLocked(winner game.Color, reason string) {
	if m.finished {
		return
	}
	redTime, blackTime := m.clockValuesLocked(time.Now().UTC())
	m.finished = true
	if m.clockTimer != nil {
		m.clockTimer.Stop()
	}
	for _, timer := range m.reconnectTimers {
		timer.Stop()
	}
	m.redRemaining, m.blackRemaining = redTime, blackTime
	m.red.push(m.eventLocked("game_over", game.Red))
	m.black.push(m.eventLocked("game_over", game.Black))
	winnerUID := m.red.uid
	if winner == game.Black {
		winnerUID = m.black.uid
	}
	record := profile.MatchRecord{ID: m.id, Mode: "pvp", RedUID: m.red.uid, BlackUID: m.black.uid, RedName: m.red.profile.VisibleName, BlackName: m.black.profile.VisibleName, WinnerUID: winnerUID, Ranked: m.ranked, Invited: m.invited, Reason: reason, InitialState: m.initial, Moves: append([]profile.MatchMove(nil), m.history...), AnalysisStatus: "processing", RedRemainingMS: redTime.Milliseconds(), BlackRemainingMS: blackTime.Milliseconds(), CreatedAt: m.createdAt, EndedAt: time.Now().UTC()}
	moves := make([]game.Move, len(m.history))
	for index := range m.history {
		moves[index] = m.history[index].Move
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if m.red.hub.store.CompleteMatch(ctx, record) == nil {
			_ = m.red.hub.store.SaveAnalysis(ctx, record.ID, bot.AnalyzeGame(record.InitialState, moves, 7))
		}
	}()
	hub := m.red.hub
	matchID := m.id
	time.AfterFunc(2*time.Minute, func() {
		m.mu.Lock()
		red, black := m.red, m.black
		m.mu.Unlock()
		hub.mu.Lock()
		if hub.matches[matchID] == m {
			delete(hub.matches, matchID)
		}
		if hub.detached[red.uid] == red {
			delete(hub.detached, red.uid)
		}
		if hub.detached[black.uid] == black {
			delete(hub.detached, black.uid)
		}
		hub.mu.Unlock()
		red.clearMatch(m)
		black.clearMatch(m)
	})
}

func (c *Client) disconnect() {
	c.closeOnce.Do(func() {
		c.hub.leaveQueues(c)
		c.hub.mu.Lock()
		if c.hub.clients[c.uid] == c {
			delete(c.hub.clients, c.uid)
		}
		if c.currentMatch() != nil {
			c.hub.detached[c.uid] = c
		}
		c.hub.mu.Unlock()
		match, color := c.matchDetails()
		if match != nil {
			match.mu.Lock()
			if !match.finished && match.player(color) == c {
				until := time.Now().UTC().Add(reconnectWindow)
				match.opponent(color).push(event{Type: "opponent_disconnected", Message: "Opponent disconnected", ReconnectUntil: until})
				match.reconnectTimers[color] = time.AfterFunc(reconnectWindow, func() {
					match.mu.Lock()
					defer match.mu.Unlock()
					if match.finished || match.player(color) != c {
						return
					}
					winner := game.Red
					if color == game.Red {
						winner = game.Black
					}
					match.state = match.state.WithWinner(winner, "opponent_disconnected")
					match.finishLocked(winner, "opponent_disconnected")
				})
			}
			match.mu.Unlock()
		}
		close(c.done)
	})
}

func (h *Hub) inviteFriend(sender *Client, friendUID string, ranked bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !h.store.AreFriends(ctx, sender.uid, friendUID) {
		sender.push(event{Type: "error", Message: "only friends can be invited"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	target := h.clients[friendUID]
	if target == nil {
		sender.push(event{Type: "error", Message: "friend is offline"})
		return
	}
	if sender.currentMatch() != nil || target.currentMatch() != nil || contains(h.casual, sender) || contains(h.ranked, sender) || contains(h.casual, target) || contains(h.ranked, target) {
		sender.push(event{Type: "error", Message: "one of you is already queued or playing"})
		return
	}
	invite := &Invite{ID: uuid.NewString(), From: sender, To: target, Ranked: ranked, CreatedAt: time.Now().UTC()}
	h.invites[invite.ID] = invite
	invite.Timer = time.AfterFunc(inviteLifetime, func() { h.expireInvite(invite.ID) })
	sender.push(event{Type: "invite_sent", InviteID: invite.ID, Ranked: ranked})
	target.push(event{Type: "invite_received", InviteID: invite.ID, Ranked: ranked, Inviter: publicProfile(sender.profile)})
}

func (h *Hub) respondInvite(recipient *Client, inviteID string, accept bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	invite := h.invites[inviteID]
	if invite == nil || invite.To.uid != recipient.uid {
		recipient.push(event{Type: "error", Message: "invitation expired"})
		return
	}
	delete(h.invites, inviteID)
	invite.Timer.Stop()
	if !accept {
		invite.From.push(event{Type: "invite_declined", InviteID: inviteID})
		return
	}
	if h.clients[invite.From.uid] != invite.From || invite.From.currentMatch() != nil || recipient.currentMatch() != nil || contains(h.casual, invite.From) || contains(h.ranked, invite.From) || contains(h.casual, recipient) || contains(h.ranked, recipient) {
		recipient.push(event{Type: "error", Message: "invitation is no longer available"})
		return
	}
	h.casual = remove(remove(h.casual, invite.From), recipient)
	h.ranked = remove(remove(h.ranked, invite.From), recipient)
	h.startMatchLocked(invite.From, recipient, invite.Ranked, true)
}

func (h *Hub) expireInvite(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if invite := h.invites[id]; invite != nil {
		delete(h.invites, id)
		invite.From.push(event{Type: "invite_expired", InviteID: id})
		invite.To.push(event{Type: "invite_expired", InviteID: id})
	}
}

func (h *Hub) Presence(uid string) (bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	client := h.clients[uid]
	return client != nil, client != nil && client.currentMatch() != nil
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
		_ = c.conn.Close()
	}
}

func (c *Client) currentMatch() *Match { c.mu.RLock(); defer c.mu.RUnlock(); return c.match }
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
func (c *Client) clearMatch(match *Match) {
	c.mu.Lock()
	if c.match == match {
		c.match = nil
		c.color = 0
	}
	c.mu.Unlock()
}
func (m *Match) player(color game.Color) *Client {
	if color == game.Red {
		return m.red
	}
	return m.black
}
func (m *Match) opponent(color game.Color) *Client {
	if color == game.Red {
		return m.black
	}
	return m.red
}

func publicProfile(value profile.Profile) *profile.Profile {
	return &profile.Profile{UID: value.UID, Username: value.Username, VisibleName: value.VisibleName, MMR: value.MMR, MatchesPlayed: value.MatchesPlayed, Wins: value.Wins, WinStreak: value.WinStreak, BestWinStreak: value.BestWinStreak}
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
