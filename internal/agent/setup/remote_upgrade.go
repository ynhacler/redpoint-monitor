package setup

// 远程升级组件的检测、OpenRC 触发器与暂存目录修复（设计 27.12、29.13）。

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RemoteUpgradeInstalled 判断本机是否安装了远程升级触发器（systemd path 单元或 OpenRC 的 crond 检查脚本）。
func RemoteUpgradeInstalled(p Paths) bool {
	for _, f := range []string{p.UpdaterPath, p.UpdaterCron} {
		if f == "" {
			continue
		}
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// RemoteUpgradeEnabled：安装了触发器且没有 no-remote-upgrade 时 Agent 才查询升级任务（设计 29.13）。
func RemoteUpgradeEnabled(p Paths) bool {
	if !RemoteUpgradeInstalled(p) {
		return false
	}
	_, err := os.Stat(p.NoRemoteUpgradeFile())
	return errors.Is(err, os.ErrNotExist)
}

// crondRunning 判断 OpenRC 主机上的 crond 是否在运行。
func crondRunning(sys System) bool {
	out, err := sys.Run("rc-service", "crond", "status")
	return err == nil && strings.Contains(out, "started")
}

// ensureCrond 确保 crond 开机启动并在运行（enable-remote-upgrade 由管理员显式执行，因此可以替他启动）。
func ensureCrond(o Options) error {
	if crondRunning(o.Sys) {
		return nil
	}
	if !o.Sys.Has("crond") {
		return errors.New("OpenRC 上的远程升级需要 crond（BusyBox 自带）：本机没有 crond，升级请执行 sudo vpsmon-agent upgrade")
	}
	if out, err := o.Sys.Run("rc-update", "add", "crond", "default"); err != nil {
		return fmt.Errorf("rc-update add crond 失败：%v %s", err, out)
	}
	if out, err := o.Sys.Run("rc-service", "crond", "start"); err != nil {
		return fmt.Errorf("启动 crond 失败：%v %s", err, out)
	}
	fmt.Fprintln(o.Out, "✓ 已启动 crond 并设为开机启动（远程升级由它定时触发）")
	return nil
}

// installUpdaterCron 写入 OpenRC 的检查脚本：crond 每 15 分钟运行，有升级请求时以 root 运行 updater。
func installUpdaterCron(o Options) error {
	if o.Paths.UpdaterCron == "" {
		return errors.New("未配置 OpenRC 远程升级脚本路径")
	}
	if err := os.MkdirAll(filepath.Dir(o.Paths.UpdaterCron), 0o755); err != nil {
		return err
	}
	return writeFile(o.Sys, o.Paths.UpdaterCron, updaterCronFile, 0o755, 0)
}

// FixStageDir 修复状态目录与暂存目录（StateDir/update）的属主，返回是否有改动。
//
// 曾以 root 运行过 Agent、或旧版本留下 root 所有的目录时，Agent 无法写入暂存文件，远程升级失败（permission denied）。
// 【安全】以 root 运行：状态目录必须是真实目录（不接受符号链接），暂存目录通过 os.Root 操作，
// 不跟随 Agent 可以控制的符号链接；只改这两个目录本身的属主与权限，不递归（设计 29.13）。
func FixStageDir(o Options) (bool, error) {
	o.defaults()
	if !o.Sys.IsRoot() {
		return false, errors.New("需要 root 权限")
	}
	uid, gid, err := o.Sys.IDs(userName)
	if err != nil {
		return false, nil // 没有 Agent 用户（用户模式或尚未安装）：不处理
	}
	state := o.Paths.StateDir
	fi, err := os.Lstat(state)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // 服务首次启动时创建
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, fmt.Errorf("状态目录 %s 不是目录（不接受符号链接）", state)
	}
	changed := false
	if owner, ok := fileOwner(fi); ok && owner != uid {
		if err := o.Sys.Chown(state, uid, gid); err != nil {
			return false, err
		}
		changed = true
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return changed, err
	}
	defer root.Close()
	const stage = "update"
	st, err := root.Lstat(stage)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return changed, nil // Agent 首次需要时自己创建
	case err != nil:
		return changed, err
	case !st.IsDir():
		// 符号链接或普通文件：删除，由 Agent 重新创建真实目录
		if err := root.Remove(stage); err != nil {
			return changed, err
		}
		return true, nil
	}
	if owner, ok := fileOwner(st); ok && owner != uid {
		if err := root.Lchown(stage, uid, gid); err != nil {
			return changed, err
		}
		changed = true
	}
	if st.Mode().Perm()&0o700 != 0o700 {
		if err := root.Chmod(stage, 0o750); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}
