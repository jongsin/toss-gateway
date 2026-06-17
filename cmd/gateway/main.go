// Command gateway 는 토스증권 Open API 게이트웨이 서버의 진입점이다.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata" // Asia/Seoul 타임존을 바이너리에 임베드 (distroless 등 tzdata 부재 이미지 대응)

	"github.com/jongsin/toss-gateway/internal/config"
	"github.com/jongsin/toss-gateway/internal/httpapi"
)

func main() {
	cfg := config.Load()

	// 컨테이너 HEALTHCHECK 용 서브커맨드: `gateway -healthcheck` → /healthz GET 후 종료코드 반환.
	if len(os.Args) > 1 && (os.Args[1] == "-healthcheck" || os.Args[1] == "healthcheck") {
		os.Exit(runHealthcheck(cfg))
	}

	logger := newLogger(cfg)

	srv := httpapi.New(cfg, logger)
	srv.StartBackground()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}

	// 종료 시그널 수신 채널
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("gateway listening",
			slog.String("addr", cfg.Addr),
			slog.String("basePath", cfg.BasePath),
			slog.String("env", cfg.Env),
			slog.String("tossBaseURL", cfg.TossBaseURL),
			slog.Bool("rateLimitEnabled", cfg.RateLimitEnabled),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("server error", slog.String("error", err.Error()))
		srv.Shutdown()
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
	}
	srv.Shutdown()
	logger.Info("gateway stopped")
}

// newLogger 는 환경에 맞는 구조화 로거를 생성한다. (production=JSON, 그 외=text)
func newLogger(cfg *config.Config) *slog.Logger {
	level := parseLevel(cfg.LogLevel)
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(cfg.Env, "production") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// runHealthcheck 는 로컬 /healthz 를 호출해 컨테이너 헬스 상태를 판정한다. (0=정상, 1=실패)
func runHealthcheck(cfg *config.Config) int {
	addr := cfg.Addr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	url := fmt.Sprintf("http://%s%s/healthz", addr, cfg.BasePath)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck error:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck status:", resp.StatusCode)
		return 1
	}
	return 0
}
