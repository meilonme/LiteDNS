package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func QueryInt(c *gin.Context, key string) (int, bool) {
	raw := c.Query(key)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		Fail(c, http.StatusBadRequest, CodeValidationErr, key+" must be an integer")
		return 0, false
	}
	return v, true
}

func QueryInt64(c *gin.Context, key string) (int64, bool) {
	raw := c.Query(key)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, CodeValidationErr, key+" must be an integer")
		return 0, false
	}
	return v, true
}

func QueryTime(c *gin.Context, key string) (*time.Time, bool) {
	raw := c.Query(key)
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		Fail(c, http.StatusBadRequest, CodeValidationErr, key+" must be RFC3339")
		return nil, false
	}
	return &t, true
}
