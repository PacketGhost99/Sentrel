// Command heartbeat-agent is the Heartbeat monitoring agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"heartbeat/agent"
	"heartbeat/internal/version"
)

const usage = `Heartbeat agent %s

Usage:
  heartbeat-agent register SERVER_URL TOKEN [--ca-fingerprint FP] [--name NAME]
  heartbeat-agent run [--config FILE]
  heartbeat-agent self-test [--config FILE]
  heartbeat-agent status [--config FILE]
  heartbeat-agent service install|uninstall|start|stop|restart [--config FILE]
  heartbeat-agent version [--short]

Register with the command shown in the web UI under "Add host".
`

func main() {
	if len(os.Args) < 2 {
		// Started by the Windows service manager without arguments.
		if !service.Interactive() {
			os.Exit(runService(agent.DefaultConfigPath()))
		}
		fmt.Fprintf(os.Stderr, usage, version.Version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "register":
		err = register(args)
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
		fs.Parse(args)
		os.Exit(runService(*cfgPath))
	case "self-test":
		err = selfTest(args)
	case "status":
		err = status(args)
	case "service":
		err = serviceCmd(args)
	case "priv":
		err = agent.PrivMain(args)
	case "version", "--version", "-v":
		if len(args) > 0 && args[0] == "--short" {
			fmt.Println(version.Version)
		} else {
			fmt.Println("heartbeat-agent", version.String())
		}
	case "help", "-h", "--help":
		fmt.Printf(usage, version.Version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n"+usage, cmd, version.Version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func logger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func register(args []string) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	server := fs.String("server", "", "server URL")
	token := fs.String("token", "", "enrolment token")
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file to write")
	stateDir := fs.String("state-dir", agent.DefaultStateDir(), "state directory (keys, certificates)")
	pin := fs.String("ca-fingerprint", "", "pin the server CA (self-signed TLS mode)")
	name := fs.String("name", "", "display name for this host")
	// Allow positional SERVER TOKEN before flags.
	var pos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos = append(pos, args[0])
		args = args[1:]
	}
	fs.Parse(args)
	pos = append(pos, fs.Args()...)
	if *server == "" && len(pos) > 0 {
		*server = pos[0]
	}
	if *token == "" && len(pos) > 1 {
		*token = pos[1]
	}
	if *server == "" || *token == "" {
		return fmt.Errorf("server URL and token are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg, err := agent.Register(ctx, agent.RegisterOptions{ServerURL: strings.TrimRight(*server, "/"), Token: *token, ConfigPath: *cfgPath,
		StateDir: *stateDir, CAFingerprint: *pin, Name: *name})
	if err != nil {
		return err
	}
	fmt.Printf("registered as %s; config written to %s\n", cfg.AgentID, *cfgPath)
	return nil
}

func selfTest(args []string) error {
	fs := flag.NewFlagSet("self-test", flag.ExitOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
	fs.Parse(args)
	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := agent.SelfTest(ctx, cfg); err != nil {
		return err
	}
	fmt.Println("ok", version.Version)
	return nil
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
	fs.Parse(args)
	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Printf("version:  %s\nserver:   %s\nagent id: %s\nstate:    %s\n", version.Version, cfg.ServerURL, cfg.AgentID, cfg.StateDir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := agent.SelfTest(ctx, cfg); err != nil {
		fmt.Println("server:   NOT REACHABLE -", err)
		return nil
	}
	fmt.Println("server:   reachable, credentials accepted")
	return nil
}

// --- service wrapper ---

type program struct {
	cfgPath string
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

func (p *program) Start(s service.Service) error {
	cfg, err := agent.LoadConfig(p.cfgPath)
	if err != nil {
		return err
	}
	a, err := agent.New(cfg, logger(cfg.LogLevel))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		p.err = a.Run(ctx)
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(15 * time.Second):
		}
	}
	return nil
}

func svcConfig(cfgPath string) *service.Config {
	return &service.Config{
		Name:        "HeartbeatAgent",
		DisplayName: "Heartbeat Agent",
		Description: "Heartbeat monitoring agent: collects metrics and runs signed jobs from the Heartbeat server.",
		Arguments:   []string{"run", "--config", cfgPath},
		Option: service.KeyValue{
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"OnFailureResetPeriod":   86400,
			"DelayedAutoStart":       true,
			"Restart":                "always",
		},
	}
}

func runService(cfgPath string) int {
	if service.Interactive() {
		// Foreground (systemd, Docker, terminal).
		cfg, err := agent.LoadConfig(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		a, err := agent.New(cfg, logger(cfg.LogLevel))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := a.Run(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	}
	prg := &program{cfgPath: cfgPath}
	s, err := service.New(prg, svcConfig(cfgPath))
	if err != nil {
		return 1
	}
	if err := s.Run(); err != nil {
		return 1
	}
	return 0
}

func serviceCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: service install|uninstall|start|stop|restart [--config FILE]")
	}
	action := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
	fs.Parse(args[1:])
	s, err := service.New(&program{cfgPath: *cfgPath}, svcConfig(*cfgPath))
	if err != nil {
		return err
	}
	return service.Control(s, action)
}
