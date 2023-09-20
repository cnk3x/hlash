package svc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kardianos/service"
)

var (
	ControlAction      = [6]string{"start", "stop", "restart", "install", "uninstall", "status"}
	ControlActionUsage = [6]string{"启动", "停止", "重启", "安装", "卸载", "状态"}
	ErrUnknownAction   = errors.New("无效的控制命令")
)

type Control func(command string) (string, error)

func (c Control) Control(command string) (string, error) {
	return c(command)
}

func (c Control) Run(command string) {
	m, err := c.Control(command)
	if m != "" {
		fmt.Println(m)
	}
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func New(prog Service) Control {
	return Control(func(command string) (m string, err error) {
		var s service.Service
		var p = program{Service: prog}
		if s, err = p.build(); err != nil {
			return
		}

		if command == "run" {
			err = s.Run()
			return
		}

		status, e := s.Status()

		switch command {
		case "start":
			if status == service.StatusStopped {
				err = s.Start()
			}
		case "stop":
			if status == service.StatusRunning {
				err = s.Stop()
			}
		case "install":
			if errors.Is(e, service.ErrNotInstalled) {
				err = s.Install()
			}
			if err == nil && status == service.StatusStopped {
				err = s.Start()
			}
		case "uninstall":
			if status == service.StatusRunning {
				err = s.Stop()
			}
			if status > 0 && err == nil {
				err = s.Uninstall()
			}
		case "status":
			if e != nil {
				if errors.Is(e, service.ErrNotInstalled) {
					m = "服务未安装"
				} else if errors.Is(e, service.ErrNoServiceSystemDetected) {
					m = "不支持的系统"
				} else {
					err = e
				}
			} else {
				switch status {
				case service.StatusRunning:
					m = "已启动"
				case service.StatusStopped:
					m = "已停止"
				}
			}
		case "restart":
			if status == service.StatusRunning {
				err = s.Stop()
			}
			if err == nil {
				err = s.Start()
			}
		default:
			err = ErrUnknownAction
		}

		if err != nil {
			if errors.Is(err, service.ErrNotInstalled) {
				m = "服务未安装"
				err = nil
			} else if errors.Is(err, service.ErrNoServiceSystemDetected) {
				m = "不支持的系统"
				err = nil
			}
			return
		}

		status, e = s.Status()
		switch command {
		case "start", "stop", "restart", "install":
			if e != nil {
				if errors.Is(e, service.ErrNotInstalled) {
					m = "服务未安装"
				} else if errors.Is(e, service.ErrNoServiceSystemDetected) {
					m = "不支持的系统"
				}
			} else {
				switch status {
				case service.StatusRunning:
					m = "已启动"
				case service.StatusStopped:
					m = "已停止"
				}
			}
		case "uninstall":
			if !errors.Is(e, service.ErrNotInstalled) {
				err = e
			} else {
				m = "已卸载"
			}
		}

		return
	})
}

type Service struct {
	Name        string   // Required name of the service. No spaces suggested.
	DisplayName string   // Display name, spaces allowed.
	Description string   // Long description of service.
	UserName    string   // Run as username.
	Arguments   []string // Run with arguments.

	// Optional field to specify the executable for service.
	// If empty the current executable is used.
	Executable string

	// Array of service dependencies.
	// Not yet fully implemented on Linux or OS X:
	//  1. Support linux-systemd dependencies, just put each full line as the
	//     element of the string array, such as
	//     "After=network.target syslog.target"
	//     "Requires=syslog.target"
	//     Note, such lines will be directly appended into the [Unit] of
	//     the generated service config file, will not check their correctness.
	Dependencies []string

	// The following fields are not supported on Windows.
	WorkingDirectory string // Initial working directory.
	ChRoot           string
	Option           map[string]any // System specific options.

	EnvVars map[string]string

	Init func() error                //block exec
	Run  func(context.Context) error //non-block exec
}

type program struct {
	Service
	cancel context.CancelFunc
}

func (p *program) build() (service.Service, error) {
	return service.New(p, &service.Config{
		Name:             p.Name,
		DisplayName:      p.DisplayName,
		Description:      p.Description,
		UserName:         p.UserName,
		Arguments:        p.Arguments,
		Executable:       p.Executable,
		Dependencies:     p.Dependencies,
		WorkingDirectory: p.WorkingDirectory,
		ChRoot:           p.ChRoot,
		Option:           p.Option,
		EnvVars:          p.EnvVars,
	})
}

func (p *program) Start(s service.Service) (err error) {
	if service.Platform() == "windows-service" {
		if p.WorkingDirectory != "" {
			if err = os.MkdirAll(p.WorkingDirectory, 0755); err != nil {
				return
			}
			if err = os.Chdir(p.WorkingDirectory); err != nil {
				return
			}
		}

		if len(p.EnvVars) > 0 {
			for k, v := range p.EnvVars {
				os.Setenv(k, v)
			}
		}
	}

	if p.Init != nil {
		if err = p.Init(); err != nil {
			return
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	p.cancel = cancel

	go func() {
		defer func() {
			if !service.Interactive() {
				s.Stop()
			}
		}()

		if p.Run != nil {
			if err := p.Run(ctx); err != nil {
				log.Println(err)
			}
		}
	}()
	return
}

func (p *program) Stop(s service.Service) (err error) {
	if p.cancel != nil {
		p.cancel()
	}
	return
}
