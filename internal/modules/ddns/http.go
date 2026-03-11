package ddns

import (
	"context"
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
	group.GET("/ddns/tasks", listHandler(service))
	group.GET("/ddns/tasks/:id", getHandler(service))
	group.POST("/ddns/tasks", createHandler(service, logsSvc))
	group.PUT("/ddns/tasks/:id", updateHandler(service, logsSvc))
	group.DELETE("/ddns/tasks/:id", deleteHandler(service, logsSvc))
	group.POST("/ddns/tasks/:id/pause", pauseHandler(service, logsSvc))
	group.POST("/ddns/tasks/:id/resume", resumeHandler(service, logsSvc))
	group.POST("/ddns/tasks/:id/run-once", runOnceHandler(service))
}

func getHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid task id")
			return
		}
		task, err := service.Get(c.Request.Context(), taskID)
		if err != nil {
			if errors.Is(err, ErrTaskNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "get ddns task failed")
			return
		}
		api.OK(c, task)
	}
}

func listHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter := ListFilter{Status: c.Query("status")}
		if raw := c.Query("domain_id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "domain_id must be integer")
				return
			}
			filter.DomainID = &id
		}

		items, err := service.List(c.Request.Context(), filter)
		if err != nil {
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "list ddns tasks failed")
			return
		}
		api.OK(c, items)
	}
}

func createHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	type request struct {
		DomainID    int64  `json:"domain_id"`
		Host        string `json:"host"`
		RecordType  string `json:"record_type"`
		IntervalSec int    `json:"interval_sec"`
	}
	return func(c *gin.Context) {
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		task, err := service.Create(c.Request.Context(), CreateInput{
			DomainID:    req.DomainID,
			Host:        req.Host,
			RecordType:  req.RecordType,
			IntervalSec: req.IntervalSec,
		})
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			return
		}
		writeTaskOperationLog(c, logsSvc, "ddns_task.create", task)
		api.OK(c, task)
	}
}

func updateHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	type request struct {
		IntervalSec *int    `json:"interval_sec"`
		Status      *string `json:"status"`
	}
	return func(c *gin.Context) {
		taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid task id")
			return
		}
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		task, err := service.Update(c.Request.Context(), taskID, UpdateInput{
			IntervalSec: req.IntervalSec,
			Status:      req.Status,
		})
		if err != nil {
			if errors.Is(err, ErrTaskNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			return
		}
		writeTaskOperationLog(c, logsSvc, "ddns_task.update", task)
		api.OK(c, task)
	}
}

func pauseHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	return statusHandler(service.Pause, logsSvc, "ddns_task.pause")
}

func deleteHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid task id")
			return
		}
		if err := service.Delete(c.Request.Context(), taskID); err != nil {
			if errors.Is(err, ErrTaskNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "delete ddns task failed")
			return
		}
		if logsSvc != nil {
			_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
				Actor:      operationActor(c),
				Action:     "ddns_task.delete",
				TargetType: "ddns_task",
				TargetID:   strconv.FormatInt(taskID, 10),
			})
		}
		api.OK(c, gin.H{})
	}
}

func resumeHandler(service *Service, logsSvc *logs.Service) gin.HandlerFunc {
	return statusHandler(service.Resume, logsSvc, "ddns_task.resume")
}

func statusHandler(fn func(ctx context.Context, taskID int64) (Task, error), logsSvc *logs.Service, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid task id")
			return
		}
		task, err := fn(c.Request.Context(), taskID)
		if err != nil {
			if errors.Is(err, ErrTaskNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, err.Error())
			return
		}
		writeTaskOperationLog(c, logsSvc, action, task)
		api.OK(c, task)
	}
}

func runOnceHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid task id")
			return
		}
		if err := service.RunOnce(c.Request.Context(), taskID); err != nil {
			if errors.Is(err, ErrTaskNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}
		api.OK(c, gin.H{"executed": true})
	}
}

func writeTaskOperationLog(c *gin.Context, logsSvc *logs.Service, action string, task Task) {
	if logsSvc == nil {
		return
	}
	payload, _ := json.Marshal(task)
	_ = logsSvc.CreateOperationLog(c.Request.Context(), logs.OperationLogInput{
		Actor:      operationActor(c),
		Action:     action,
		TargetType: "ddns_task",
		TargetID:   strconv.FormatInt(task.ID, 10),
		DetailJSON: string(payload),
	})
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
