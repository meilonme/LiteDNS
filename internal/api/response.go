package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeOK              = "OK"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeValidationErr   = "VALIDATION_ERROR"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeUpstreamErr     = "UPSTREAM_ERROR"
	CodeUpstreamTimeout = "UPSTREAM_TIMEOUT"
	CodeInternalErr     = "INTERNAL_ERROR"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"code":    CodeOK,
		"message": "success",
		"data":    data,
	})
}

func Fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, Error{
		Code:      code,
		Message:   message,
		RequestID: requestID(),
	})
}

func requestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
