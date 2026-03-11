package settings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"litedns/internal/api"
	"litedns/internal/modules/auth"
	"litedns/internal/modules/logs"
)

func RegisterRoutes(group *gin.RouterGroup, service *Service, logsSvc *logs.Service) {
	group.GET("/settings", getHandler(service))
	group.PUT("/settings", updateHandler(service, logsSvc))
	group.POST("/settings/public-ip-check/run-once", runPublicIPCheckOnceHandler(service, logsSvc))
}

func getHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vals, err := service.Get(c.Request.Context())
		if err != nil {
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "load settings failed")
			return
		}
		api.OK(c, vals)
	}
}

func updateHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req UpdateInput
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		vals, err := service.Update(c.Request.Context(), req)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			return
		}

		if logsSvc != nil {
			if payload, err := json.Marshal(req); err == nil {
				_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
					Actor:      operationActor(c),
					Action:     "settings.update",
					TargetType: "settings",
					TargetID:   "global",
					DetailJSON: string(payload),
				})
			}
		}
		api.OK(c, vals)
	}
}

func runPublicIPCheckOnceHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.RunPublicIPCheckNow(c.Request.Context(), nil); err != nil {
			if errors.Is(err, ErrPublicIPCheckDisabled) {
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
				return
			}
			if errors.Is(err, ErrPublicIPCheckRunning) {
				api.Fail(c, http.StatusConflict, api.CodeConflict, err.Error())
				return
			}
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}

		if logsSvc != nil {
			_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
				Actor:      operationActor(c),
				Action:     "settings.public_ip_check.run_once",
				TargetType: "settings",
				TargetID:   "public_ip_check",
			})
		}
		api.OK(c, gin.H{"executed": true})
	}
}

func operationActor(c *gin.Context) string {
	admin, ok := auth.CurrentAdmin(c)
	if !ok || admin.ID <= 0 {
		return "system"
	}
	if admin.Username != "" {
		return admin.Username
	}
	return "admin#" + strconv.FormatInt(admin.ID, 10)
}
