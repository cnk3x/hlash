package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/Dreamacro/clash/config"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/hub"
	"github.com/Dreamacro/clash/hub/executor"
	"github.com/Dreamacro/clash/log"
	"github.com/cnk3x/hlash/hlash/svc"
	"github.com/spf13/pflag"

	"go.uber.org/automaxprocs/maxprocs"
)

func main() {
	maxprocs.Set(maxprocs.Logger(func(string, ...any) {}))

	var homeDir, secret, ctlEndpoint, ctlUI string
	var subscribeUrl string
	var updateWait time.Duration
	var run bool
	var svcAct string
	var mixedPort int

	pflag.ErrHelp = errors.New("")
	pflag.CommandLine.Init(filepath.Base(os.Args[0]), pflag.ExitOnError)
	pflag.StringVarP(&homeDir, "home", "d", homeDir, "数据目录")
	pflag.StringVarP(&secret, "secret", "k", secret, "控制端口密钥")
	pflag.StringVar(&ctlEndpoint, "ctl", ctlEndpoint, "控制端口地址")
	pflag.StringVar(&ctlUI, "ui", "ui", "控制面板路径")
	pflag.StringVarP(&subscribeUrl, "subscribe", "u", subscribeUrl, "订阅地址")
	pflag.DurationVarP(&updateWait, "subscribe_interval", "t", updateWait, "订阅地址更新间隔时间")
	pflag.BoolVar(&run, "run", run, "启动clash")
	pflag.StringVarP(&svcAct, "svc", "s", "run", "服务")
	pflag.IntVarP(&mixedPort, "mixedPort", "p", 0, "端口")
	pflag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintf(os.Stderr, "  %s [...options]\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		pflag.PrintDefaults()
	}
	pflag.Parse()

	var args []string
	if svcAct == "install" {
		args = slices.DeleteFunc(os.Args[1:], func(arg string) bool { return arg == "install" || arg == "--svc" || arg == "-s" })
	}

	sc := svc.New(svc.Service{
		Name:             "hlash",
		DisplayName:      "hlash",
		Description:      "可以自动更新订阅的clash服务",
		Arguments:        args,
		WorkingDirectory: homeDir,
		Run: func(ctx context.Context) (err error) {
			C.SetHomeDir(homeDir)
			if run {
				subscribeUpdateImmediate(ctx, homeDir, subscribeUrl)
				go subscribeUpdate(ctx, homeDir, subscribeUrl, updateWait, true)
				clashRun(ctx, homeDir, mixedPort, secret, ctlEndpoint, ctlUI)
			} else {
				subscribeUpdate(ctx, homeDir, subscribeUrl, updateWait, true)
			}
			return
		},
	})

	sc.Run(svcAct)
}

// 运行
func clashRun(ctx context.Context, homeDir string, mixedPort int, secret, ctlEndpoint, ctlUI string) {
	if err := config.Init(C.Path.HomeDir()); err != nil {
		log.Fatalln("Initial configuration directory error: %s", err.Error())
	}

	var options []hub.Option
	{
		if secret != "" {
			options = append(options, hub.WithSecret(secret))
		}

		if ctlEndpoint != "" {
			options = append(options, hub.WithExternalController(ctlEndpoint))
		}

		if ctlUI != "" {
			options = append(options, hub.WithExternalUI(ctlUI))
		}

		if mixedPort > 0 {
			options = append(options, func(c *config.Config) {
				c.General.MixedPort = 7890
				c.General.Port = 0
				c.General.SocksPort = 0
			})
		}

		if err := hub.Parse(options...); err != nil {
			log.Fatalln("Parse config error: %s", err.Error())
		}
	}

	hupSign := make(chan os.Signal, 1)
	signal.Notify(hupSign, syscall.SIGHUP)

	for {
		select {
		case <-ctx.Done():
			return
		case <-hupSign:
			if cfg, err := executor.ParseWithPath(C.Path.Config()); err == nil {
				executor.ApplyConfig(cfg, true)
			} else {
				log.Errorln("Parse config error: %s", err.Error())
			}
		}
	}
}

// 更新订阅
func subscribeUpdateImmediate(ctx context.Context, homeDir, subscribeUrl string) (err error) {
	if subscribeUrl == "" {
		return
	}

	download := func(saveTo string) (err error) {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}

		log.Infoln("download: %s", subscribeUrl)
		var req *http.Request
		var resp *http.Response

		if req, err = http.NewRequestWithContext(ctx, http.MethodGet, subscribeUrl, nil); err != nil {
			return
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.31")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6,zh-TW;q=0.5")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")

		client := &http.Client{
			Timeout: time.Second * 10,
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				Proxy:                 http.ProxyFromEnvironment,
				TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			},
		}

		var ok bool
		for i := 0; i < 10; i++ {
			if i > 0 {
				sleep := min(time.Second*1<<i, time.Second*15)
				log.Infoln("sleep %s", sleep)
				select {
				case <-ctx.Done():
					err = ctx.Err()
					return
				case <-time.After(sleep):
				}
			}
			if resp, err = client.Do(req); err != nil {
				if i < 9 {
					log.Errorln("get error(%d): %v", i, err)
					continue
				}
				return
			}

			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				err = fmt.Errorf("status error: %s", resp.Status)
				if resp.StatusCode > 404 && i < 9 {
					log.Errorln("status error(%d): %s", i, resp.Status)
					continue
				}
				return
			}

			ok = true
			break
		}

		if !ok {
			return
		}

		if err = readToFile(resp.Body, saveTo); err != nil {
			return
		}
		return
	}

	configTest := func(fn string) (err error) {
		_, err = executor.ParseWithPath(fn)
		return
	}

	var (
		target = C.Path.Resolve(C.Path.Config())
		tempDl = target + ".update"
		backup string
	)

	log.Infoln("config update...")
	if err = download(tempDl); err != nil {
		log.Errorln("config download: %v", err)
		return
	}

	if err = configTest(tempDl); err != nil {
		log.Errorln("config test: %v", err)
		_ = configTest(C.Path.Config())
		return
	}

	if stat, _ := os.Stat(target); stat != nil {
		backup = target + "-" + time.Now().Format("20060102-150405") + ".backup"
		if err = os.Rename(target, backup); err != nil {
			log.Errorln("config backup: %v", err)
			return
		}
	}

	if err = os.Rename(tempDl, target); err != nil {
		log.Errorln("config save: %v", err)
		return
	}

	return
}

// 更新订阅
func subscribeUpdate(ctx context.Context, homeDir, subscribeUrl string, wait time.Duration, reloadRequire bool) (err error) {
	reload := func() error {
		if !reloadRequire {
			return nil
		}
		proc, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return proc.Signal(syscall.SIGHUP)
	}

	ticker := time.NewTicker(wait)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err = subscribeUpdateImmediate(ctx, homeDir, subscribeUrl); err != nil {
				log.Errorln("config update: %v", err)
			} else if err = reload(); err != nil {
				log.Errorln("config reload: %v", err)
			}
			log.Infoln("config next update at %s", time.Now().Add(wait).Format(time.RFC3339))
		}
	}
}

func readToFile(src io.Reader, saveTo string) (err error) {
	os.MkdirAll(filepath.Dir(saveTo), 0755)

	var dst *os.File
	if dst, err = os.Create(saveTo); err != nil {
		log.Errorln("%v", err)
		return
	}
	defer dst.Close()

	if _, err = io.Copy(dst, src); err != nil {
		log.Errorln("%v", err)
		return
	}
	return
}
