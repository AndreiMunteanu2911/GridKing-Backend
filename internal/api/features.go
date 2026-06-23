package api

import (
	"net/http"
	"strconv"
	"strings"

	"GridKing-Backend/internal/auth"
	"GridKing-Backend/internal/game"
	"github.com/gin-gonic/gin"
)

func (s *Server) matches(c *gin.Context) {
	uid, _ := auth.UID(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	values, err := s.profiles.ListMatches(c.Request.Context(), uid, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load match history"})
		return
	}
	c.JSON(http.StatusOK, values)
}

func (s *Server) match(c *gin.Context) {
	uid, _ := auth.UID(c)
	value, err := s.profiles.GetMatch(c.Request.Context(), uid, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "match not found"})
		return
	}
	c.JSON(http.StatusOK, value)
}

func (s *Server) achievements(c *gin.Context) {
	uid, _ := auth.UID(c)
	values, err := s.profiles.Achievements(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load achievements"})
		return
	}
	c.JSON(http.StatusOK, values)
}

func (s *Server) friends(c *gin.Context) {
	uid, _ := auth.UID(c)
	values, err := s.profiles.Friends(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load friends"})
		return
	}
	for index := range values {
		if !values[index].Profile.PresenceVisible {
			continue
		}
		values[index].Online, values[index].InGame = s.hub.Presence(values[index].Profile.UID)
	}
	c.JSON(http.StatusOK, values)
}

func (s *Server) friendRequests(c *gin.Context) {
	uid, _ := auth.UID(c)
	values, err := s.profiles.FriendRequests(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load friend requests"})
		return
	}
	c.JSON(http.StatusOK, values)
}

func (s *Server) searchPlayers(c *gin.Context) {
	uid, _ := auth.UID(c)
	values, err := s.profiles.SearchUsers(c.Request.Context(), uid, c.Query("q"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not search players"})
		return
	}
	c.JSON(http.StatusOK, values)
}

func (s *Server) sendFriendRequest(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		UID string `json:"uid"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "player is required"})
		return
	}
	if err := s.profiles.SendFriendRequest(c.Request.Context(), uid, request.UID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) respondFriendRequest(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		UID    string `json:"uid"`
		Accept bool   `json:"accept"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid response"})
		return
	}
	if err := s.profiles.RespondFriendRequest(c.Request.Context(), uid, request.UID, request.Accept); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) removeFriend(c *gin.Context) {
	uid, _ := auth.UID(c)
	if err := s.profiles.RemoveFriend(c.Request.Context(), uid, c.Param("uid")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not remove friend"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) setPresenceVisibility(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		Visible bool `json:"visible"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid preference"})
		return
	}
	if err := s.profiles.SetPresenceVisibility(c.Request.Context(), uid, request.Visible); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save preference"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) dailyPuzzle(c *gin.Context) {
	uid, _ := auth.UID(c)
	puzzle, err := s.profiles.DailyPuzzle(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not prepare today's puzzle"})
		return
	}
	puzzle.LegalMoves = puzzle.State.LegalMoves()
	if !puzzle.Completed {
		puzzle.BestMove = game.Move{}
	}
	c.JSON(http.StatusOK, puzzle)
}

func (s *Server) completePuzzle(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		Move game.Move `json:"move"`
	}
	if c.ShouldBindJSON(&request) != nil || len(request.Move.Path) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a complete move is required"})
		return
	}
	correct, err := s.profiles.CompletePuzzle(c.Request.Context(), uid, strings.TrimSpace(c.Param("id")), request.Move)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not check puzzle"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"correct": correct})
}
