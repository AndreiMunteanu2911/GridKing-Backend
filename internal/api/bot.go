package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"GridKing-Backend/internal/auth"
	"GridKing-Backend/internal/bot"
	"GridKing-Backend/internal/game"
	"GridKing-Backend/internal/profile"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type botSession struct {
	mu          sync.Mutex
	id          string
	uid         string
	state       game.State
	initial     game.State
	history     []profile.MatchMove
	human       game.Color
	difficulty  bot.Difficulty
	personality bot.Personality
	createdAt   time.Time
}

type BotManager struct {
	mu       sync.RWMutex
	sessions map[string]*botSession
	store    BotMatchStore
}

type BotMatchStore interface {
	SaveBotMatch(context.Context, profile.MatchRecord) error
	SaveAnalysis(context.Context, string, []game.MoveAnalysis) error
}

func NewBotManager(store BotMatchStore) *BotManager {
	return &BotManager{sessions: make(map[string]*botSession), store: store}
}

func (m *BotManager) Start(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		Difficulty  bot.Difficulty  `json:"difficulty"`
		Personality bot.Personality `json:"personality"`
		Color       string          `json:"color"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	human, err := game.ParseColor(request.Color)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	personality, err := bot.ParsePersonality(request.Personality)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := bot.ValidateDifficulty(request.Difficulty); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	state := game.NewState()
	initial := state
	history := make([]profile.MatchMove, 0)
	if human == game.Black {
		move, chooseErr := bot.Choose(state, request.Difficulty, personality)
		if chooseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": chooseErr.Error()})
			return
		}
		before := state
		state, _ = state.Apply(move)
		history = append(history, profile.MatchMove{Ply: 1, Color: before.Turn, Move: move, CapturedPieces: before.CapturedPieces(move), State: state, PlayedAt: time.Now().UTC()})
	}
	session := &botSession{id: uuid.NewString(), uid: uid, state: state, initial: initial, history: history, human: human, difficulty: request.Difficulty, personality: personality, createdAt: time.Now().UTC()}
	m.mu.Lock()
	m.sessions[uid] = session
	m.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"match_id": session.id, "state": state, "legal_moves": state.LegalMoves(), "color": human, "history": history, "personality": personality})
}

func (m *BotManager) Move(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		Move game.Move `json:"move"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	m.mu.RLock()
	session := m.sessions[uid]
	m.mu.RUnlock()
	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "start a bot game first"})
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.Winner != 0 || session.state.Turn != session.human {
		c.JSON(http.StatusConflict, gin.H{"error": "the game is over or it is not your turn"})
		return
	}
	beforeHuman := session.state
	next, err := beforeHuman.Apply(request.Move)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	session.history = append(session.history, profile.MatchMove{Ply: len(session.history) + 1, Color: beforeHuman.Turn, Move: request.Move, CapturedPieces: beforeHuman.CapturedPieces(request.Move), State: next, PlayedAt: time.Now().UTC()})
	if next.Winner == 0 {
		botMove, chooseErr := bot.Choose(next, session.difficulty, session.personality)
		if chooseErr == nil {
			beforeBot := next
			next, _ = beforeBot.Apply(botMove)
			session.history = append(session.history, profile.MatchMove{Ply: len(session.history) + 1, Color: beforeBot.Turn, Move: botMove, CapturedPieces: beforeBot.CapturedPieces(botMove), State: next, PlayedAt: time.Now().UTC()})
		}
	}
	session.state = next
	if next.Winner != 0 {
		m.persist(session)
		m.mu.Lock()
		if m.sessions[uid] == session {
			delete(m.sessions, uid)
		}
		m.mu.Unlock()
	}
	c.JSON(http.StatusOK, gin.H{"match_id": session.id, "state": next, "legal_moves": next.LegalMoves(), "color": session.human, "history": session.history, "personality": session.personality})
}

func (m *BotManager) persist(session *botSession) {
	history := append([]profile.MatchMove(nil), session.history...)
	moves := make([]game.Move, len(history))
	for index := range history {
		moves[index] = history[index].Move
	}
	redUID, blackUID := "bot", "bot"
	if session.human == game.Red {
		redUID = session.uid
	} else {
		blackUID = session.uid
	}
	winnerUID := "bot"
	if session.state.Winner == session.human {
		winnerUID = session.uid
	}
	redName, blackName := "GridKing Bot", "GridKing Bot"
	if session.human == game.Red {
		redName = "You"
	} else {
		blackName = "You"
	}
	record := profile.MatchRecord{ID: session.id, Mode: "bot", RedUID: redUID, BlackUID: blackUID, RedName: redName, BlackName: blackName, WinnerUID: winnerUID, Reason: session.state.Reason, InitialState: session.initial, Moves: history, AnalysisStatus: "processing", BotDifficulty: string(session.difficulty), BotPersonality: string(session.personality), CreatedAt: session.createdAt, EndedAt: time.Now().UTC()}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if m.store.SaveBotMatch(ctx, record) == nil {
			analysis := bot.AnalyzeGame(session.initial, moves, 7)
			_ = m.store.SaveAnalysis(ctx, session.id, analysis)
		}
	}()
}
