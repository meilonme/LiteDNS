package http

import (
	"github.com/gin-gonic/gin"

	"litedns/internal/modules/auth"
	"litedns/internal/modules/ddns"
	"litedns/internal/modules/domain"
	"litedns/internal/modules/logs"
	"litedns/internal/modules/record"
	"litedns/internal/modules/settings"
	"litedns/internal/modules/vendor"
)

type Dependencies struct {
	Auth     *auth.Service
	Vendor   *vendor.Service
	Domain   *domain.Service
	Record   *record.Service
	DDNS     *ddns.Service
	Logs     *logs.Service
	Settings *settings.Service
}

func NewRouter(deps Dependencies) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		api.GET("/ping", func(c *gin.Context) {
			c.JSON(200, gin.H{"message": "pong"})
		})

		auth.RegisterRoutes(api, deps.Auth)

		protected := api.Group("/")
		protected.Use(auth.AuthMiddleware(deps.Auth))
		vendor.RegisterRoutes(protected, deps.Vendor, deps.Logs)
		domain.RegisterRoutes(protected, deps.Domain)
		record.RegisterRoutes(protected, deps.Record, deps.Logs)
		ddns.RegisterRoutes(protected, deps.DDNS, deps.Logs)
		logs.RegisterRoutes(protected, deps.Logs)
		settings.RegisterRoutes(protected, deps.Settings, deps.Logs)
	}

	return r
}
