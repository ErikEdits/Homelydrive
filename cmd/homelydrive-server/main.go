// Command homelydrive-server is the Homelydrive cloud server for Windows and
// Linux. It runs as a background service and is reachable through Tailscale.
//
//	homelydrive-server                 run in the foreground
//	homelydrive-server gui             open the server window
//	homelydrive-server service install install as Windows service / systemd unit
//	homelydrive-server user add NAME   create a user (see "help")
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/erikedits/homelydrive/internal/appwindow"
	"github.com/erikedits/homelydrive/internal/server/api"
	"github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/db"
	"github.com/erikedits/homelydrive/internal/server/logbuf"
	"github.com/erikedits/homelydrive/internal/server/tailnet"
	"github.com/erikedits/homelydrive/internal/server/webdavfs"
	"github.com/erikedits/homelydrive/internal/version"
	"github.com/erikedits/homelydrive/web"
)

type tlsConfig = tls.Config

const usage = `Homelydrive Server %s

Aufruf / Usage:
  homelydrive-server [--data DIR] [run]        Server im Vordergrund starten
  homelydrive-server [--data DIR] gui          Server-Fenster öffnen
  homelydrive-server service install           Als Dienst installieren (Windows-Dienst / systemd)
  homelydrive-server service uninstall|start|stop|restart|status
  homelydrive-server user add NAME [--admin] [--quota-gb N]
  homelydrive-server user passwd NAME
  homelydrive-server user list
  homelydrive-server user delete NAME
  homelydrive-server version

Datenordner / data folder: %s
`

func main() {
	attachConsole()
	fs := flag.NewFlagSet("homelydrive-server", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data folder (config, database, thumbnails)")
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, version.Version, config.DefaultDataDir()) }
	fs.Parse(os.Args[1:])
	args := fs.Args()
	cmd := "run"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = runService(*dataDir, "")
	case "gui":
		err = openGUI(*dataDir)
	case "service":
		err = serviceCmd(*dataDir, args)
	case "user":
		err = userCmd(*dataDir, args)
	case "version", "--version":
		fmt.Println(version.Version)
	case "help", "-h", "--help":
		fs.Usage()
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		showError(err.Error())
		os.Exit(1)
	}
}

// ----- running the server -----

type program struct {
	dataDir string
	cancel  context.CancelFunc
	done    chan struct{}
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := serve(ctx, p.dataDir); err != nil {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

func serviceConfig(dataDir string) *service.Config {
	exe, _ := os.Executable()
	return &service.Config{
		Name:        "homelydrive",
		DisplayName: "Homelydrive Server",
		Description: "Eigene Cloud (Google-Drive-Alternative) über Tailscale – https://github.com/" + version.Repo,
		Executable:  exe,
		Arguments:   []string{"--data", dataDir, "run"},
		Option: service.KeyValue{
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"Restart":                "always",
			"After":                  "network-online.target tailscaled.service",
		},
	}
}

func runService(dataDir, _ string) error {
	prg := &program{dataDir: dataDir}
	svc, err := service.New(prg, serviceConfig(dataDir))
	if err != nil {
		return err
	}
	if !service.Interactive() {
		return svc.Run()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return serve(ctx, dataDir)
}

// serve runs the server until ctx is cancelled.
func serve(ctx context.Context, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logs := logbuf.New(2000, filepath.Join(dataDir, "logs"), os.Stderr)
	level := slog.LevelInfo
	if os.Getenv("HOMELYDRIVE_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)
	log.Info("starting Homelydrive server", "version", version.Version, "data", dataDir)

	database, err := db.Open(dataDir)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer database.Close()
	c, err := core.New(database, cfg, dataDir, log)
	if err != nil {
		return err
	}
	if err := firstRunFromEnv(c, log); err != nil {
		return err
	}
	c.Start(ctx)

	cert := &tailnet.CertManager{Dir: filepath.Join(dataDir, "tls"), Log: log}
	srv := &api.Server{
		Core:    c,
		Cfg:     cfg,
		Log:     log,
		Logs:    logs,
		Web:     web.FS(),
		Cert:    cert,
		Started: time.Now(),
		DAV:     webdavfs.New(c, log),
	}
	if srv.Web == nil {
		log.Warn("web UI is not built into this binary (run `npm run build` in web/)")
	}
	conf := cfg.Get()
	mgr := &tailnet.Manager{
		Port:    conf.Port,
		Handler: srv.Handler(),
		Fixed:   conf.ListenAddrs,
		Log:     log,
		TLS: func() *tlsConfig {
			if !cfg.Get().HTTPS {
				return nil
			}
			return cert.Config()
		},
	}
	srv.Listeners = mgr
	if len(conf.ListenAddrs) == 0 && len(tailnet.LocalIPs()) == 0 {
		log.Warn("no Tailscale address found yet - is Tailscale installed and connected? Only localhost is available until it is.")
	}
	mgr.Run(ctx)
	log.Info("server stopped")
	return nil
}

// firstRunFromEnv creates the first admin from environment variables
// (useful for Docker): HOMELYDRIVE_ADMIN_USER and HOMELYDRIVE_ADMIN_PASSWORD.
func firstRunFromEnv(c *core.Core, log *slog.Logger) error {
	user, pw := os.Getenv("HOMELYDRIVE_ADMIN_USER"), os.Getenv("HOMELYDRIVE_ADMIN_PASSWORD")
	if user == "" || pw == "" || c.UserCount() > 0 {
		return nil
	}
	if _, err := c.CreateUser(core.NewUser{Username: user, Password: pw, IsAdmin: true}); err != nil {
		return fmt.Errorf("creating admin from environment: %w", err)
	}
	log.Info("admin user created from environment", "user", user)
	return nil
}

// ----- server window -----

func openGUI(dataDir string) error {
	port := config.DefaultPort
	if cfg, err := config.Load(dataDir); err == nil {
		port = cfg.Get().Port
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/admin/", port)
	if !ping(port) {
		// Not running yet: try to start the installed service.
		if svc, err := service.New(&program{dataDir: dataDir}, serviceConfig(dataDir)); err == nil {
			svc.Start()
		}
		for i := 0; i < 20 && !ping(port); i++ {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if !ping(port) {
		// No service: run the server inside this process while the window is open.
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		errc := make(chan error, 1)
		go func() { errc <- serve(ctx, dataDir) }()
		for i := 0; i < 40 && !ping(port); i++ {
			time.Sleep(250 * time.Millisecond)
		}
		cmd, err := appwindow.Open(url, filepath.Join(userConfigDir(), "server-window"), 1150, 780)
		if err != nil {
			return err
		}
		if cmd != nil {
			go func() { cmd.Wait(); cancel() }()
		}
		return <-errc
	}
	_, err := appwindow.Open(url, filepath.Join(userConfigDir(), "server-window"), 1150, 780)
	return err
}

func userConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "Homelydrive")
}

func ping(port int) bool {
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/ping", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ----- service management -----

func serviceCmd(dataDir string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: homelydrive-server service install|uninstall|start|stop|restart|status")
	}
	dataDir, _ = filepath.Abs(dataDir)
	svc, err := service.New(&program{dataDir: dataDir}, serviceConfig(dataDir))
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		st, err := svc.Status()
		if err != nil {
			return err
		}
		fmt.Println(map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped"}[st])
		return nil
	case "install":
		if err := svc.Install(); err != nil {
			return err
		}
		fmt.Println("Dienst installiert. Starte ihn mit: homelydrive-server service start")
		return nil
	default:
		return service.Control(svc, args[0])
	}
}

// ----- user management on the command line -----

func openCore(dataDir string) (*core.Core, func(), error) {
	cfg, err := config.Load(dataDir)
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(dataDir)
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c, err := core.New(database, cfg, dataDir, log)
	if err != nil {
		database.Close()
		return nil, nil, err
	}
	return c, func() { database.Close() }, nil
}

func readPassword(prompt string) string {
	if pw := os.Getenv("HOMELYDRIVE_PASSWORD"); pw != "" {
		return pw
	}
	fmt.Fprint(os.Stderr, prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func userCmd(dataDir string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: homelydrive-server user add|passwd|list|delete")
	}
	c, closeFn, err := openCore(dataDir)
	if err != nil {
		return err
	}
	defer closeFn()
	switch args[0] {
	case "list":
		users, err := c.ListUsers()
		if err != nil {
			return err
		}
		for _, u := range users {
			admin := ""
			if u.IsAdmin {
				admin = " (admin)"
			}
			fmt.Printf("%-20s %s%s  %d GB\n", u.Username, u.DisplayName, admin, u.QuotaBytes>>30)
		}
		return nil
	case "add":
		fs := flag.NewFlagSet("user add", flag.ExitOnError)
		admin := fs.Bool("admin", false, "make the user an admin")
		quota := fs.Int64("quota-gb", -1, "storage limit in GB (0 = unlimited)")
		name := ""
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			name = args[1]
			fs.Parse(args[2:])
		} else {
			fs.Parse(args[1:])
			name = fs.Arg(0)
		}
		if name == "" {
			return errors.New("usage: homelydrive-server user add NAME [--admin] [--quota-gb N]")
		}
		pw := readPassword("Passwort für " + name + ": ")
		nu := core.NewUser{Username: name, Password: pw, IsAdmin: *admin}
		if *quota >= 0 {
			q := *quota << 30
			nu.QuotaBytes = &q
		}
		u, err := c.CreateUser(nu)
		if err != nil {
			return err
		}
		fmt.Printf("Benutzer %s angelegt.\n", u.Username)
		return nil
	case "passwd":
		if len(args) < 2 {
			return errors.New("usage: homelydrive-server user passwd NAME")
		}
		u, err := c.GetUserByName(args[1])
		if err != nil {
			return err
		}
		pw := readPassword("Neues Passwort für " + u.Username + ": ")
		return c.UpdateUser(u.ID, core.UserUpdate{Password: &pw}, "")
	case "delete":
		if len(args) < 2 {
			return errors.New("usage: homelydrive-server user delete NAME")
		}
		u, err := c.GetUserByName(args[1])
		if err != nil {
			return err
		}
		return c.DeleteUser(u.ID, false)
	}
	return errors.New("unknown user command: " + args[0])
}
