package record

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"litedns/internal/api"
	"litedns/internal/modules/auth"
	"litedns/internal/modules/domain"
	"litedns/internal/modules/logs"
)

func RegisterRoutes(group *gin.RouterGroup, service *Service, logsSvc *logs.Service) {
	group.POST("/domains/:id/records", createHandler(service, logsSvc))
	group.PUT("/records/:id", updateHandler(service, logsSvc))
	group.DELETE("/records/:id", deleteHandler(service, logsSvc))
}

func createHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	type request struct {
		Host    string `json:"host"`
		Type    string `json:"type"`
		Value   string `json:"value"`
		TTL     int    `json:"ttl"`
		Proxied bool   `json:"proxied"`
		Line    string `json:"line"`
	}
	return func(c *gin.Context) {
		domainID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid domain id")
			return
		}
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		item, err := service.Create(c.Request.Context(), domainID, CreateInput{
			Host:    req.Host,
			Type:    req.Type,
			Value:   req.Value,
			TTL:     req.TTL,
			Proxied: req.Proxied,
			Line:    req.Line,
		})
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrDomainNotFound):
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
			default:
				api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			}
			return
		}
		if logsSvc != nil {
			if payload, err := json.Marshal(item); err == nil {
				_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
					Actor:      operationActor(c),
					Action:     "record.create",
					TargetType: "record",
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
		Value   *string `json:"value"`
		TTL     *int    `json:"ttl"`
		Proxied *bool   `json:"proxied"`
		Line    *string `json:"line"`
	}
	return func(c *gin.Context) {
		recordID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid record id")
			return
		}
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}

		item, err := service.Update(c.Request.Context(), recordID, UpdateInput{
			Value:   req.Value,
			TTL:     req.TTL,
			Proxied: req.Proxied,
			Line:    req.Line,
		})
		if err != nil {
			switch {
			case errors.Is(err, ErrRecordNotFound):
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
			default:
				api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			}
			return
		}
		if logsSvc != nil {
			if payload, err := json.Marshal(item); err == nil {
				_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
					Actor:      operationActor(c),
					Action:     "record.update",
					TargetType: "record",
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
		recordID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid record id")
			return
		}
		if err := service.Delete(c.Request.Context(), recordID); err != nil {
			if errors.Is(err, ErrRecordNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}
		if logsSvc != nil {
			_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
				Actor:      operationActor(c),
				Action:     "record.delete",
				TargetType: "record",
				TargetID:   strconv.FormatInt(recordID, 10),
			})
		}
		api.OK(c, gin.H{})
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
