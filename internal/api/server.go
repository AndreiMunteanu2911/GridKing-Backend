package api

import (
	"net/http"
	"strconv"
	"strings"

	"GridKing-Backend/internal/auth"
	"GridKing-Backend/internal/profile"
	"GridKing-Backend/internal/realtime"
	firebaseauth "firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
)

type Server struct {
	profiles *profile.Store
	verifier *auth.Verifier
	identity *auth.IdentityService
	auth     *firebaseauth.Client
	hub      *realtime.Hub
	bots     *BotManager
	origin   string
}

func NewServer(profiles *profile.Store, verifier *auth.Verifier, identity *auth.IdentityService, authClient *firebaseauth.Client, hub *realtime.Hub, origin string) *Server {
	return &Server{profiles: profiles, verifier: verifier, identity: identity, auth: authClient, hub: hub, bots: NewBotManager(), origin: strings.TrimRight(origin, "/")}
}

func (s *Server) Router() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), s.cors())
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.POST("/auth/register", s.register)
	router.POST("/auth/login", s.login)
	router.POST("/auth/refresh", s.refresh)
	router.GET("/ws", s.websocket)
	secured := router.Group("/api", s.verifier.Middleware())
	secured.POST("/profiles", s.createProfile)
	secured.POST("/auth/logout", s.logout)
	secured.GET("/profiles/me", s.getProfile)
	secured.GET("/leaderboard", s.leaderboard)
	secured.POST("/bot/start", s.bots.Start)
	secured.POST("/bot/move", s.bots.Move)
	return router
}

func (s *Server) register(c *gin.Context) {
	var request struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		Username    string `json:"username"`
		VisibleName string `json:"visible_name"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	created, err := s.auth.CreateUser(c.Request.Context(), (&firebaseauth.UserToCreate{}).Email(strings.TrimSpace(request.Email)).Password(request.Password))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not create account"})
		return
	}
	value, err := s.profiles.Create(c.Request.Context(), profile.Profile{UID: created.UID, Username: request.Username, VisibleName: request.VisibleName})
	if err != nil {
		_ = s.auth.DeleteUser(c.Request.Context(), created.UID)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	session, err := s.identity.SignIn(c.Request.Context(), request.Email, request.Password)
	if err != nil {
		c.JSON(http.StatusCreated, gin.H{"profile": value, "message": "account created; sign in to continue"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"profile": value, "user": gin.H{"uid": session.UID, "email": session.Email}, "id_token": session.IDToken, "refresh_token": session.RefreshToken, "expires_in": session.ExpiresIn})
}

func (s *Server) login(c *gin.Context) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	session, err := s.identity.SignIn(c.Request.Context(), request.Email, request.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid email or password"})
		return
	}
	value, err := s.profiles.Get(c.Request.Context(), session.UID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "profile not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"profile": value, "user": gin.H{"uid": session.UID, "email": session.Email}, "id_token": session.IDToken, "refresh_token": session.RefreshToken, "expires_in": session.ExpiresIn})
}

func (s *Server) refresh(c *gin.Context) {
	var request struct {
		RefreshToken string `json:"refresh_token"`
	}
	if c.ShouldBindJSON(&request) != nil || request.RefreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh token is required"})
		return
	}
	session, err := s.identity.Refresh(c.Request.Context(), request.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": gin.H{"uid": session.UID}, "id_token": session.IDToken, "refresh_token": session.RefreshToken, "expires_in": session.ExpiresIn})
}

func (s *Server) logout(c *gin.Context) {
	uid, _ := auth.UID(c)
	if err := s.auth.RevokeRefreshTokens(c.Request.Context(), uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not end session"})
		return
	}
	c.Status(http.StatusNoContent)
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
