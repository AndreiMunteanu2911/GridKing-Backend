package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	firebaseauth "firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
)

const UIDKey = "uid"

type Verifier struct{ client *firebaseauth.Client }

func New(client *firebaseauth.Client) *Verifier { return &Verifier{client: client} }

func (v *Verifier) Verify(ctx context.Context, raw string) (string, error) {
	token, err := v.client.VerifyIDToken(ctx, raw)
	if err != nil {
		return "", err
	}
	return token.UID, nil
}

func (v *Verifier) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		uid, err := v.Verify(c.Request.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set(UIDKey, uid)
		c.Next()
	}
}

func UID(c *gin.Context) (string, error) {
	value, exists := c.Get(UIDKey)
	if !exists {
		return "", errors.New("authenticated uid is unavailable")
	}
	uid, ok := value.(string)
	if !ok || uid == "" {
		return "", errors.New("authenticated uid is invalid")
	}
	return uid, nil
}
