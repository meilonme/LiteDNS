package vendor

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
	group.GET("/vendors", listHandler(service))
	group.POST("/vendors", createHandler(service, logsSvc))
	group.PUT("/vendors/:id", updateHandler(service, logsSvc))
	group.DELETE("/vendors/:id", deleteHandler(service, logsSvc))
	group.POST("/vendors/:id/verify", verifyHandler(service))
}

func listHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := service.List(c.Request.Context())
		if err != nil {
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "list vendors failed")
			return
		}
		api.OK(c, items)
	}
}

func createHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	type request struct {
		Name      string `json:"name"`
		Provider  string `json:"provider"`
		APIKey    string `json:"api_key"`
		APISecret string `json:"api_secret"`
		Extra     string `json:"extra"`
	}
	return func(c *gin.Context) {
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		item, err := service.Create(c.Request.Context(), CreateInput{
			Name:      req.Name,
			Provider:  req.Provider,
			APIKey:    req.APIKey,
			APISecret: req.APISecret,
			ExtraJSON: req.Extra,
		})
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			return
		}
		if logsSvc != nil {
			if payload, err := json.Marshal(item); err == nil {
				_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
					Actor:      operationActor(c),
					Action:     "vendor.create",
					TargetType: "vendor",
					TargetID:   strconv.FormatInt(item.ID, 10),
					DetailJSON: string(payload),
				})
			}
		}
		api.OK(c, item)
	}
}

func updateHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	type request struct {
		Name      *string `json:"name"`
		Provider  *string `json:"provider"`
		APIKey    *string `json:"api_key"`
		APISecret *string `json:"api_secret"`
		Extra     *string `json:"extra"`
	}
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid vendor id")
			return
		}
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}

		item, err := service.Update(c.Request.Context(), id, UpdateInput{
			Name:      req.Name,
			Provider:  req.Provider,
			APIKey:    req.APIKey,
			APISecret: req.APISecret,
			ExtraJSON: req.Extra,
		})
		if err != nil {
			switch {
			case errors.Is(err, ErrNotFound):
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
			default:
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			}
			return
		}
		if logsSvc != nil {
			if payload, err := json.Marshal(item); err == nil {
				_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
					Actor:      operationActor(c),
					Action:     "vendor.update",
					TargetType: "vendor",
					TargetID:   strconv.FormatInt(item.ID, 10),
					DetailJSON: string(payload),
				})
			}
		}
		api.OK(c, item)
	}
}

func deleteHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid vendor id")
			return
		}
		err = service.Delete(c.Request.Context(), id)
		if err != nil {
			switch {
			case errors.Is(err, ErrNotFound):
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
			case errors.Is(err, ErrConflict):
				api.Fail(c, http.StatusConflict, api.CodeConflict, err.Error())
			default:
				api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "delete vendor failed")
			}
			return
		}
		if logsSvc != nil {
			_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
				Actor:      operationActor(c),
				Action:     "vendor.delete",
				TargetType: "vendor",
				TargetID:   strconv.FormatInt(id, 10),
			})
		}
		api.OK(c, gin.H{})
	}
}

func verifyHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid vendor id")
			return
		}
		if err := service.Verify(c.Request.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}
		api.OK(c, gin.H{"verified": true})
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
