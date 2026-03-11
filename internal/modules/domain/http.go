package domain

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"litedns/internal/api"
)

func RegisterRoutes(group *gin.RouterGroup, service *Service) {
	group.GET("/domains", listDomainsHandler(service))
	group.POST("/domains/:id/sync", syncDomainHandler(service))
	group.GET("/domains/:id/records", listRecordsHandler(service))
}

func listDomainsHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var vendorID *int64
		if raw := c.Query("vendor_id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "vendor_id must be integer")
				return
			}
			vendorID = &id
		}
		items, err := service.ListDomains(c.Request.Context(), vendorID)
		if err != nil {
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}
		api.OK(c, items)
	}
}

func syncDomainHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		domainID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid domain id")
			return
		}
		summary, err := service.SyncDomainRecords(c.Request.Context(), domainID)
		if err != nil {
			if errors.Is(err, ErrDomainNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}
		api.OK(c, summary)
	}
}

func listRecordsHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		domainID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid domain id")
			return
		}
		items, err := service.ListRecords(c.Request.Context(), domainID)
		if err != nil {
			if errors.Is(err, ErrDomainNotFound) {
				api.Fail(c, http.StatusNotFound, api.CodeNotFound, err.Error())
				return
			}
			api.Fail(c, http.StatusBadGateway, api.CodeUpstreamErr, err.Error())
			return
		}
		api.OK(c, items)
	}
}
