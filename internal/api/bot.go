package api

import (
	"net/http"
	"sync"

	"GridKing-Backend/internal/auth"
	"GridKing-Backend/internal/bot"
	"GridKing-Backend/internal/game"
	"github.com/gin-gonic/gin"
)

type botSession struct {
	mu         sync.Mutex
	state      game.State
	human      game.Color
	difficulty bot.Difficulty
}

type BotManager struct {
	mu       sync.RWMutex
	sessions map[string]*botSession
}

func NewBotManager() *BotManager { return &BotManager{sessions: make(map[string]*botSession)} }

func (m *BotManager) Start(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		Difficulty bot.Difficulty `json:"difficulty"`
		Color      string         `json:"color"`
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
	state := game.NewState()
	if human == game.Black {
		move, chooseErr := bot.Choose(state, request.Difficulty)
		if chooseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": chooseErr.Error()})
			return
		}
		state, _ = state.Apply(move)
	}
	session := &botSession{state: state, human: human, difficulty: request.Difficulty}
	m.mu.Lock()
	m.sessions[uid] = session
	m.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"state": state, "legal_moves": state.LegalMoves(), "color": human})
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
	next, err := session.state.Apply(request.Move)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if next.Winner == 0 {
		botMove, chooseErr := bot.Choose(next, session.difficulty)
		if chooseErr == nil {
			next, _ = next.Apply(botMove)
		}
	}
	session.state = next
	c.JSON(http.StatusOK, gin.H{"state": next, "legal_moves": next.LegalMoves(), "color": session.human})
}
