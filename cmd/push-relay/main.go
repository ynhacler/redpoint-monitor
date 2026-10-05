// push-relay：官方 Push Relay（设计 30.2），把面板的加密推送无状态地转发给 APNs / FCM。
//
// 【隐私】不写访问日志、不记录请求内容、Push Token、实例公钥与来源 IP（设计 30.5）；只打印启动信息与
// 每小时一次的聚合计数。前面的反向代理同样需要关闭访问日志。
//
// 用法（凭证只通过文件传入，不出现在命令行中，设计 27.1）：
//
//	push-relay --listen 127.0.0.1:8090 \
//	  --apns-key /etc/push-relay/AuthKey_XXXX.p8 --apns-key-id XXXX --apns-team-id YYYY --apns-topic dev.vpsmon.app \
//	  --fcm-service-account /etc/push-relay/fcm.json
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vpsmon/internal/relay"
)

var version = "0.1.0-dev"

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "listen address (put an HTTPS reverse proxy with access logs disabled in front)")
	apnsKey := flag.String("apns-key", "", "path to the APNs auth key (.p8); empty disables APNs")
	apnsKeyID := flag.String("apns-key-id", "", "APNs key ID")
	apnsTeam := flag.String("apns-team-id", "", "Apple developer team ID")
	apnsTopic := flag.String("apns-topic", "", "App bundle ID")
	apnsSandbox := flag.Bool("apns-sandbox", false, "use the APNs sandbox (development builds)")
	fcmSA := flag.String("fcm-service-account", "", "path to the Firebase service account JSON; empty disables FCM")
	perMinute := flag.Int("per-minute", 30, "messages per instance per minute")
	perDay := flag.Int("per-day", 2000, "messages per instance per day")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg := relay.Config{PerMinute: *perMinute, PerDay: *perDay}
	if *apnsKey != "" {
		raw, err := os.ReadFile(*apnsKey)
		if err != nil {
			log.Fatalf("read APNs key: %v", err)
		}
		k, err := relay.ParseAPNsKey(raw)
		if err != nil {
			log.Fatal(err)
		}
		if *apnsKeyID == "" || *apnsTeam == "" || *apnsTopic == "" {
			log.Fatal("--apns-key-id, --apns-team-id and --apns-topic are required with --apns-key")
		}
		cfg.APNs = &relay.APNsConfig{KeyID: *apnsKeyID, TeamID: *apnsTeam, Topic: *apnsTopic, Key: k}
		if *apnsSandbox {
			cfg.APNs.Endpoint = "https://api.sandbox.push.apple.com"
		}
	}
	if *fcmSA != "" {
		raw, err := os.ReadFile(*fcmSA)
		if err != nil {
			log.Fatalf("read FCM service account: %v", err)
		}
		if cfg.FCM, err = relay.ParseFCMServiceAccount(raw); err != nil {
			log.Fatal(err)
		}
	}
	if cfg.APNs == nil && cfg.FCM == nil {
		log.Fatal("neither APNs nor FCM is configured")
	}

	r := relay.New(cfg)
	srv := &http.Server{Addr: *listen, Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, ErrorLog: log.New(discard{}, "", 0)} // 不输出含来源地址的连接错误
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	go func() { // 每小时一次聚合计数，不含任何实例或设备信息
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				log.Printf("stats %s", r.Stats())
			}
		}
	}()
	log.Printf("push-relay %s listening on %s (apns=%v fcm=%v, %d/min, %d/day per instance)",
		version, *listen, cfg.APNs != nil, cfg.FCM != nil, *perMinute, *perDay)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
