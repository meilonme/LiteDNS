package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litedns/internal/config"
	"litedns/internal/db"
	apphttp "litedns/internal/http"
	"litedns/internal/modules/auth"
	"litedns/internal/modules/ddns"
	"litedns/internal/modules/domain"
	"litedns/internal/modules/logs"
	"litedns/internal/modules/record"
	"litedns/internal/modules/settings"
	"litedns/internal/modules/vendor"
	"litedns/internal/provider"
	"litedns/internal/provider/aliyun"
	"litedns/internal/provider/cloudflare"
	"litedns/internal/scheduler"
	"litedns/internal/security"
)

type App struct {
	cfg     config.Config
	runner  func(addr string) error
	cleanup func()
}

func New() (*App, error) {
	if err := ensureContainerConfig(); err != nil {
		return nil, fmt.Errorf("ensure container config: %w", err)
	}

	configPath := strings.TrimSpace(os.Getenv("LITEDNS_CONFIG_PATH"))
	if configPath == "" {
		configPath = filepath.Join("configs", "config.yaml")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	masterKey, err := security.LoadMasterKey()
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}

	conn, err := db.Open(cfg.DB.Path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Migrate(context.Background(), conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	providerManager := provider.NewManager(
		aliyun.New(),
		cloudflare.New(),
	)

	logsSvc := logs.NewService(conn)
	settingsSvc := settings.NewService(conn, cfg, logsSvc)
	authSvc := auth.NewService(conn, cfg.Auth.SessionTTLSeconds, cfg.Auth.ForceChangePassword)
	vendorSvc := vendor.NewService(conn, masterKey, providerManager)
	domainSvc := domain.NewService(conn, providerManager, vendorSvc, cfg.Sync.TTLSeconds)
	recordSvc := record.NewService(conn, domainSvc, providerManager)
	ddnsSvc := ddns.NewService(conn, providerManager, domainSvc, settingsSvc, logsSvc)

	username, password, created, err := authSvc.EnsureBootstrapAdmin(context.Background())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ensure bootstrap admin: %w", err)
	}

	passwordFileResult, err := authSvc.ApplyAdminPasswordFile(context.Background(), filepath.Join("configs", "admin-password"))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("apply admin password file: %w", err)
	}
	if passwordFileResult.DeleteErr != nil {
		log.Printf("warning: admin password file could not be deleted: %v", passwordFileResult.DeleteErr)
	}
	if passwordFileResult.Found {
		if created {
			log.Printf("bootstrap admin created: username=%s password file applied", username)
		}
		if passwordFileResult.Changed {
			log.Printf("admin password reset from password file")
		}
	} else if created {
		log.Printf("bootstrap admin created: username=%s password=%s", username, password)
	}

	router, err := apphttp.NewRouter(apphttp.Dependencies{
		Auth:     authSvc,
		Vendor:   vendorSvc,
		Domain:   domainSvc,
		Record:   recordSvc,
		DDNS:     ddnsSvc,
		Logs:     logsSvc,
		Settings: settingsSvc,
	}, cfg.Server.TrustedProxies)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("build router: %w", err)
	}

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	jobScheduler := scheduler.New(ddnsSvc, time.Second, 4, log.Default())
	go jobScheduler.Start(schedulerCtx)

	ipCheckCtx, cancelIPCheck := context.WithCancel(context.Background())
	go settingsSvc.StartPublicIPChecker(ipCheckCtx, time.Second, log.Default())

	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	go runLogCleanup(cleanupCtx, logsSvc, cfg.Logs.RetentionDays)

	return &App{
		cfg: cfg,
		runner: func(addr string) error {
			return router.Run(addr)
		},
		cleanup: func() {
			cancelScheduler()
			cancelIPCheck()
			cancelCleanup()
			_ = conn.Close()
		},
	}, nil
}

func (a *App) Run() error {
	defer a.cleanup()
	addr := fmt.Sprintf("%s:%d", a.cfg.Server.Host, a.cfg.Server.Port)
	return a.runner(addr)
}

func runLogCleanup(ctx context.Context, logsSvc *logs.Service, retentionDays int) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := logsSvc.Cleanup(context.Background(), retentionDays); err != nil {
				log.Printf("log cleanup failed: %v", err)
			}
		}
	}
}

func ensureContainerConfig() error {
	const (
		appDir      = "/app"
		configDir   = "/app/configs"
		targetPath  = "/app/configs/config.yaml"
		examplePath = "/app/configs/config.example.yaml"
	)

	if _, err := os.Stat(appDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(targetPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if _, err := os.Stat(examplePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	src, err := os.Open(examplePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	log.Printf("generated config from example: %s <- %s", targetPath, examplePath)
	return nil
}
