package main

// 面板启动参数的环境变量与端口（设计 28.1）：
//   - run 的每个参数都可以用环境变量 VPSMON_<参数名大写、连字符改下划线> 设置，例如 VPSMON_LISTEN、VPSMON_PUBLIC_URL；
//     命令行优先于环境变量，环境变量优先于默认值。systemd / OpenRC 通过 /etc/vpsmon/server.env 传入，升级不会覆盖。
//   - 监听地址可以只写端口：--listen 9090 等同于 127.0.0.1:9090（默认只监听本机，由反向代理对外）；
//     --https-listen / --http-listen 只写端口时监听所有地址（内置 HTTPS 需要对外）。

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// envName 返回参数对应的环境变量名：public-url → VPSMON_PUBLIC_URL。
func envName(flagName string) string {
	return "VPSMON_" + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// applyEnv 把未在命令行出现的参数按环境变量设置；值不合法时返回错误（启动失败，不静默忽略）。
func applyEnv(fs *flag.FlagSet, getenv func(string) string) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		if err != nil || set[f.Name] {
			return
		}
		if v := getenv(envName(f.Name)); v != "" {
			if e := fs.Set(f.Name, v); e != nil {
				err = fmt.Errorf("环境变量 %s 的值不正确：%v", envName(f.Name), e)
			}
		}
	})
	return err
}

// normalizeListen 校验监听地址；只写端口时补上 defaultHost（"" 表示所有地址）。空字符串原样返回（表示关闭）。
func normalizeListen(v, defaultHost string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(v); err == nil {
		v = net.JoinHostPort(defaultHost, v)
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", fmt.Errorf("监听地址 %q 格式不正确，应为 端口、主机:端口 或 [IPv6]:端口", v)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("监听地址 %q 的端口应为 1～65535", v)
	}
	return net.JoinHostPort(host, port), nil
}

// lookupEnv 便于测试替换
var lookupEnv = os.Getenv
