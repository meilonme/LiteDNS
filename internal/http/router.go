package http

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

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

func NewRouter(deps Dependencies, trustedProxies []string) (*gin.Engine, error) {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}

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

	registerWebRoutes(r)

	return r, nil
}

func registerWebRoutes(r *gin.Engine) {
	staticDir := findStaticDir()
	if staticDir == "" {
		return
	}

	r.Static("/assets", filepath.Join(staticDir, "assets"))
	r.GET("/", func(c *gin.Context) {
		c.File(filepath.Join(staticDir, "index.html"))
	})
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api/" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.File(filepath.Join(staticDir, "index.html"))
	})
}

func findStaticDir() string {
	candidates := []string{
		"web",
		"frontend/dist",
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
