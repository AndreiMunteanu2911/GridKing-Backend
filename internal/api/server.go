package api

import (
	"net/http"
	"strconv"
	"strings"

	"GridKing-Backend/internal/auth"
	"GridKing-Backend/internal/profile"
	"GridKing-Backend/internal/realtime"
	"github.com/gin-gonic/gin"
)

type Server struct {
	profiles *profile.Store
	verifier *auth.Verifier
	hub      *realtime.Hub
	bots     *BotManager
	origin   string
}

func NewServer(profiles *profile.Store, verifier *auth.Verifier, hub *realtime.Hub, origin string) *Server {
	return &Server{profiles: profiles, verifier: verifier, hub: hub, bots: NewBotManager(), origin: strings.TrimRight(origin, "/")}
}

func (s *Server) Router() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), s.cors())
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.GET("/ws", s.websocket)
	secured := router.Group("/api", s.verifier.Middleware())
	secured.POST("/profiles", s.createProfile)
	secured.GET("/profiles/me", s.getProfile)
	secured.GET("/leaderboard", s.leaderboard)
	secured.POST("/bot/start", s.bots.Start)
	secured.POST("/bot/move", s.bots.Move)
	return router
}

func (s *Server) createProfile(c *gin.Context) {
	uid, _ := auth.UID(c)
	var request struct {
		Username    string `json:"username"`
		VisibleName string `json:"visible_name"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	value, err := s.profiles.Create(c.Request.Context(), profile.Profile{UID: uid, Username: request.Username, VisibleName: request.VisibleName})
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, value)
}

func (s *Server) getProfile(c *gin.Context) {
	uid, _ := auth.UID(c)
	value, err := s.profiles.Get(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}
	c.JSON(http.StatusOK, value)
}

func (s *Server) leaderboard(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	values, err := s.profiles.Leaderboard(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load leaderboard"})
		return
	}
	c.JSON(http.StatusOK, values)
}

func (s *Server) websocket(c *gin.Context) {
	token := c.Query("token")
	uid, err := s.verifier.Verify(c.Request.Context(), token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
		return
	}
	user, err := s.profiles.Get(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "profile is required"})
		return
	}
	_ = s.hub.Serve(c.Writer, c.Request, user)
}

func (s *Server) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.origin != "" {
			c.Header("Access-Control-Allow-Origin", s.origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
