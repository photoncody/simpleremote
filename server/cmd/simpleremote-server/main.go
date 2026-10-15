// Command simpleremote-server runs the SimpleRemote web server.
//
//	simpleremote-server                 run the server (configured by env vars)
//	simpleremote-server hash-password   print a bcrypt hash for ADMIN_PASSWORD_HASH
//	simpleremote-server healthcheck     exit 0 if the local server answers /healthz
//	simpleremote-server version         print the version
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/photoncody/simpleremote/server/internal/api"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/dist"
	"github.com/photoncody/simpleremote/server/internal/session"
	"github.com/photoncody/simpleremote/server/internal/store"
	"github.com/photoncody/simpleremote/web"
)

var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "hash-password":
		err = hashPassword()
	case "healthcheck":
		err = healthcheck()
	case "version", "--version", "-v":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (expected serve, hash-password, healthcheck, or version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open database in %s: %w", cfg.DataDir, err)
	}
	defer st.Close()

	authSvc := auth.New(cfg, st, log)
	mgr, err := session.New(ctx, cfg, st, log)
	if err != nil {
		return err
	}
	d := dist.New(cfg.AgentDir)
	if len(d.Available()) == 0 {
		log.Warn("no agent builds found; end users won't be able to download the agent", "agent_dir", cfg.AgentDir)
	}
	srv := &api.Server{Config: cfg, Store: st, Auth: authSvc, Sessions: mgr, Dist: d, Log: log, Web: web.FS()}
	handler := srv.Handler()

	go maintenance(ctx, cfg, st, authSvc, mgr, srv, log)

	hs := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("SimpleRemote server started", "version", version, "listen", cfg.ListenAddr, "public_url", cfg.PublicURL.String(),
		"oidc", cfg.OIDC.Enabled(), "local_admin", cfg.Admin.Enabled(), "agents", len(d.Available()))

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

func maintenance(ctx context.Context, cfg *config.Config, st *store.Store, a *auth.Service, m *session.Manager, srv *api.Server, log *slog.Logger) {
	reap := time.NewTicker(10 * time.Second)
	sweep := time.NewTicker(10 * time.Minute)
	defer reap.Stop()
	defer sweep.Stop()
	purge := func() {
		if cfg.AuditRetentionDays == 0 {
			return
		}
		cutoff := time.Now().AddDate(0, 0, -cfg.AuditRetentionDays)
		if err := st.Purge(ctx, cutoff); err != nil {
			log.Error("purge audit log", "err", err)
		}
	}
	purge()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reap.C:
			m.Reap()
		case <-sweep.C:
			a.Sweep(ctx)
			srv.Sweep()
			purge()
		}
	}
}

func hashPassword() error {
	var pw string
	if fi, _ := os.Stdin.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Password (input is visible): ")
	}
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		pw = strings.TrimRight(sc.Text(), "\r\n")
	}
	if len(pw) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	fmt.Println(h)
	fmt.Fprintln(os.Stderr, "In docker-compose .env files, escape each $ as $$ or use ADMIN_PASSWORD_HASH_FILE.")
	return nil
}

func healthcheck() error {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
