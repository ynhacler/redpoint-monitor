package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"vpsmon/internal/release"
)

// 特权 updater（设计 29.13）：由 systemd path 单元在 request.json 出现时以 root 启动，执行一次后退出。
//
// 约束：
//   - 不联网，不解析来自面板的任何指令；request.json 视为不可信，只取任务 ID 与目标版本
//   - 从暂存目录读取清单、签名与构建（不跟随符号链接），用自身内置公钥重新完整校验，拒绝降级
//   - 只替换 /usr/local/bin/vpsmon-agent 这一个路径；工作目录与备份在只有 root 可写的 RootWorkDir
//   - 自身不通过远程升级更新（安装在单独的路径，只随 install 或本机 upgrade 更新）
//   - 节点配置了禁止远程升级（DisableFile 存在）时直接拒绝

// UpdaterOptions 是 updater 的参数。
type UpdaterOptions struct {
	StageDir    string // Agent 的暂存目录
	WorkDir     string // RootWorkDir
	StateDir    string // Agent 状态目录（status.json）
	Bin         string
	DisableFile string // 存在时拒绝远程升级，如 /etc/vpsmon-agent/no-remote-upgrade
	Keys        []release.PublicKey
	Restart     func() error
	ReadStatus  func() (string, int64, error)
	Out         io.Writer
	// 测试用
	HealthTimeout time.Duration
	Now           func() time.Time
	Sleep         func(time.Duration)
}

// RunUpdater 处理暂存目录中的升级请求，并写下 result.json 供 Agent 上报。没有请求时直接返回。
func RunUpdater(ctx context.Context, o UpdaterOptions) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	stage, err := openStage(o.StageDir)
	if err != nil || stage == nil {
		return err
	}
	defer stage.Close()
	req, err := readRequest(o.StageDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		removeStage(stage)
		return writeResult(stage, Result{Status: "failed", Reason: "升级请求无效：" + err.Error()})
	}
	res := Result{TaskID: req.TaskID, To: req.Version}
	finish := func(status, reason string) error {
		res.Status, res.Reason = status, reason
		fmt.Fprintf(o.Out, "upgrade task %d: %s %s\n", req.TaskID, status, reason)
		removeStage(stage)
		return writeResult(stage, res)
	}
	if o.DisableFile != "" {
		if _, err := os.Stat(o.DisableFile); err == nil {
			return finish("failed", "本节点已禁止远程升级（"+o.DisableFile+"）")
		}
	}
	if _, err := release.ParseVersion(req.Version); err != nil {
		return finish("failed", "目标版本无效")
	}
	// 当前版本取自已安装的程序本身（root 所有，只由本流程用已验签的构建替换），而不是 Agent 可写的状态文件
	cur, err := installedVersion(ctx, o.Bin)
	if err != nil {
		return finish("failed", "无法读取当前版本："+err.Error())
	}
	res.From = cur
	err = Run(ctx, Options{Current: cur, Target: req.Version, Keys: o.Keys, Bin: o.Bin, StateDir: o.StateDir,
		WorkDir: o.WorkDir, Source: DirSource{Dir: o.StageDir}, Restart: o.Restart, ReadStatus: o.ReadStatus, Out: o.Out,
		HealthTimeout: o.HealthTimeout, Now: o.Now, Sleep: o.Sleep})
	var rb *RollbackError
	switch {
	case err == nil:
		return finish("success", "")
	case errors.As(err, &rb):
		return finish("rolled_back", rb.Reason)
	default:
		return finish("failed", err.Error())
	}
}

func installedVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// openStage 打开暂存目录。【安全】暂存目录由 Agent 用户所有，Agent 可以把它换成指向别处的符号链接；
// updater 以 root 在其中删除与写入文件，因此先确认它是真实目录，再通过 os.Root 操作，
// 打开后再比对一次，保证此后的删除与写入都不会落到目录之外（设计 29.13）。
func openStage(dir string) (*os.Root, error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("暂存目录 %s 不是目录（不接受符号链接）", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if now, err := root.Stat("."); err != nil || !os.SameFile(fi, now) {
		root.Close()
		return nil, fmt.Errorf("暂存目录 %s 在检查期间被替换", dir)
	}
	return root, nil
}

// stageFiles 是暂存目录中 updater 会删除的文件；只删除这些已知名字，不按目录遍历。
func stageFiles() []string {
	return []string{"request.json", "request.json.tmp", "manifest.json", "manifest.json.minisig",
		"vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel(), "vpsmon-agent-" + runtime.GOOS + "-" + ArchLabel() + ".part"}
}

// removeStage 删除暂存的请求与文件（结果文件除外）。
func removeStage(root *os.Root) {
	for _, f := range stageFiles() {
		root.Remove(f)
	}
}

// writeResult 写下 result.json（不跟随符号链接，O_EXCL 创建；内容一次写入）。
func writeResult(root *os.Root, r Result) error {
	b, _ := json.Marshal(r)
	root.Remove("result.json")
	f, err := root.OpenFile("result.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
