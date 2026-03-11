package logs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"litedns/internal/api"
)

func RegisterRoutes(group *gin.RouterGroup, service *Service) {
	group.GET("/logs", listHandler(service))
}

func listHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var filter Filter
		filter.Type = c.Query("type")
		filter.Result = c.Query("result")

		if rawTaskID := c.Query("ddns_task_id"); rawTaskID != "" {
			taskID, ok := api.QueryInt64(c, "ddns_task_id")
			if !ok {
				return
			}
			if taskID <= 0 {
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "ddns_task_id must be positive")
				return
			}
			filter.DDNSTaskID = &taskID
		}

		start, ok := api.QueryTime(c, "start")
		if !ok {
			return
		}
		end, ok := api.QueryTime(c, "end")
		if !ok {
			return
		}
		filter.Start = start
		filter.End = end

		items, err := service.List(c.Request.Context(), filter)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			return
		}
		api.OK(c, items)
	}
}
