package setup

// 服务管理兼容（设计 28）：systemd 与 OpenRC（Alpine 等小型系统）。安装、卸载、状态、重启与刷新服务文件
// 都经过这里；已安装的系统按存在的服务文件判断，不重复探测。

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
)

// openrcScript 是 OpenRC 服务脚本，与 deploy/openrc/vpsmon-agent 保持一致（由测试保证）。
//
//go:embed vpsmon-agent.openrc
var openrcScript string

// initKind 是主机的 init 系统。
type initKind string

const (
	initSystemd initKind = "systemd"
	initOpenRC  initKind = "openrc"
)

// detectInit 选择安装时使用的 init 系统：systemd 优先，其次 OpenRC。
func detectInit(sys System) (initKind, error) {
	switch {
	case sys.HasSystemd():
		return initSystemd, nil
	case sys.HasOpenRC():
		return initOpenRC, nil
	}
	return "", errors.New("未检测到 systemd 或 OpenRC：目前支持这两种 init 系统（SysVinit / runit 见设计 28）；" +
		"也可以前台运行：vpsmon-agent --server … --token-file …")
}

// installedInit 返回已安装的服务所用的 init 系统；未安装时返回空。
func installedInit(p Paths) initKind {
	if _, err := os.Stat(p.Unit); err == nil {
		return initSystemd
	}
	if _, err := os.Stat(p.InitScript); err == nil {
		return initOpenRC
	}
	return ""
}

// serviceFile 返回 init 系统对应的服务文件路径、内容与权限。
func serviceFile(p Paths, k initKind) (path, content string, mode os.FileMode) {
	if k == initOpenRC {
		return p.InitScript, openrcScript, 0o755
	}
	return p.Unit, unitFile, 0o644
}

// enableAndStart 在写入服务文件后启用开机启动并立即启动。
func enableAndStart(sys System, k initKind) error {
	if k == initOpenRC {
		if out, err := sys.Run("rc-update", "add", serviceName, "default"); err != nil {
			return fmt.Errorf("rc-update add 失败：%v %s", err, out)
		}
		if out, err := sys.Run("rc-service", serviceName, "start"); err != nil {
			return fmt.Errorf("启动服务失败：%v %s", err, out)
		}
		return nil
	}
	if out, err := sys.Run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败：%v %s", err, out)
	}
	if out, err := sys.Run("systemctl", "enable", "--now", serviceName); err != nil {
		return fmt.Errorf("启动服务失败：%v %s", err, out)
	}
	return nil
}

// stopAndDisable 停止服务并取消开机启动（服务文件由调用方删除）。尽力而为，忽略错误。
func stopAndDisable(sys System, k initKind) {
	switch k {
	case initOpenRC:
		sys.Run("rc-service", serviceName, "stop")
		sys.Run("rc-update", "del", serviceName, "default")
	case initSystemd:
		sys.Run("systemctl", "disable", "--now", serviceName)
	}
}

// restartService 重启已安装的服务。
func restartService(sys System, k initKind) error {
	var out string
	var err error
	switch k {
	case initOpenRC:
		out, err = sys.Run("rc-service", serviceName, "restart")
	case initSystemd:
		out, err = sys.Run("systemctl", "restart", serviceName)
	default:
		return errors.New("未安装 Agent 服务")
	}
	if err != nil {
		return fmt.Errorf("重启服务失败：%v %s", err, out)
	}
	return nil
}

// RestartInstalled 重启本机已安装的 Agent 服务（本机升级后使用）。
func RestartInstalled(o Options) error {
	o.defaults()
	return restartService(o.Sys, installedInit(o.Paths))
}

// Installed 判断本机是否安装了 Agent 服务（systemd 或 OpenRC）。
func Installed(o Options) bool {
	o.defaults()
	return installedInit(o.Paths) != ""
}

// serviceState 返回服务状态的简短描述，如 active / started / inactive。
func serviceState(sys System, k initKind) string {
	if k == initOpenRC {
		out, err := sys.Run("rc-service", serviceName, "status")
		if s := strings.TrimSpace(out); s != "" {
			// 输出形如 " * status: started"
			if i := strings.LastIndex(s, ":"); i >= 0 {
				return strings.TrimSpace(s[i+1:]) + "（OpenRC）"
			}
			return s
		}
		if err != nil {
			return "stopped（OpenRC）"
		}
		return "unknown（OpenRC）"
	}
	out, _ := sys.Run("systemctl", "is-active", serviceName)
	return strings.TrimSpace(out)
}

// logHint 返回查看 Agent 日志的命令：systemd 写入 journal；OpenRC 下经 logger 写入 syslog。
func logHint(k initKind) string {
	if k == initOpenRC {
		return "logread -e " + serviceName + "（或 grep " + serviceName + " /var/log/messages）"
	}
	return "journalctl -u " + serviceName + " -n 50"
}

// createUser 创建运行 Agent 的系统用户（不可登录、无主目录），返回撤销函数。
// 有 useradd（shadow）时用它；Alpine 等只有 BusyBox 的系统用 addgroup / adduser（设计 28）。
func createUser(sys System) (undo func(), err error) {
	shell := nologinShell()
	if sys.Has("useradd") {
		if out, err := sys.Run("useradd", "--system", "--no-create-home", "--shell", shell, userName); err != nil {
			return nil, fmt.Errorf("创建用户 %s 失败：%v %s", userName, err, out)
		}
		return func() { sys.Run("userdel", userName) }, nil
	}
	if sys.Has("adduser") && sys.Has("addgroup") {
		if out, err := sys.Run("addgroup", "-S", userName); err != nil {
			return nil, fmt.Errorf("创建用户组 %s 失败：%v %s", userName, err, out)
		}
		if out, err := sys.Run("adduser", "-S", "-D", "-H", "-h", "/var/empty", "-s", shell, "-G", userName, userName); err != nil {
			sys.Run("delgroup", userName)
			return nil, fmt.Errorf("创建用户 %s 失败：%v %s", userName, err, out)
		}
		return func() { deleteUser(sys) }, nil
	}
	return nil, errors.New("找不到 useradd 或 adduser，无法创建运行 Agent 的系统用户")
}

// deleteUser 删除系统用户（及 BusyBox 下单独创建的同名用户组）。尽力而为。
func deleteUser(sys System) {
	if sys.Has("userdel") {
		sys.Run("userdel", userName)
		return
	}
	sys.Run("deluser", userName)
	sys.Run("delgroup", userName)
}

// nologinShell 返回本机的 nologin 路径：Debian 系为 /usr/sbin/nologin，Alpine 为 /sbin/nologin。
func nologinShell() string {
	for _, p := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/bin/false"
}
