package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var goarch = runtime.GOARCH

// Status 是 Agent 每次上报后写入 status.json 的运行状态，供 vpsmon-agent status 显示（设计 24.5）。
// 【安全】不包含 Token 或任何凭证。
type Status struct {
	Version     string `json:"version"`
	Server      string `json:"server"`
	LastAttempt int64  `json:"last_attempt"` // Unix 秒
	LastSuccess int64  `json:"last_success"` // Unix 秒；0 表示从未成功
	LastError   string `json:"last_error"`   // 最近一次失败的原因；成功后清空
}

// WriteStatus 原子写入 status.json。dir 为空或不存在时直接忽略（例如在 Mac 上开发），
// 状态文件只是辅助信息，写入失败不能影响上报。
func WriteStatus(dir string, st Status) {
	if dir == "" {
		return
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	b, _ := json.Marshal(st)
	tmp := filepath.Join(dir, ".status.json.tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(dir, "status.json"))
}

// ReadStatus 读取 status.json。
func ReadStatus(path string) (*Status, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st Status
	return &st, json.Unmarshal(b, &st)
}

// readEnv 读取 install 写入的 env 文件（KEY=VALUE，每行一项）。
func readEnv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			out[k] = v
		}
	}
	return out
}

// PrintStatus 执行 vpsmon-agent status：服务状态、所属面板、节点名、最近一次上报（设计 27.11）。
// 不需要 root；env 文件不可读时只显示能看到的部分。
func PrintStatus(o Options) error {
	o.defaults()
	p := o.Paths
	if _, err := os.Stat(p.Unit); err != nil {
		fmt.Fprintln(o.Out, "未安装：找不到 "+p.Unit)
		return errors.New("not installed")
	}
	env := readEnv(p.envFile())
	active, _ := o.Sys.Run("systemctl", "is-active", serviceName)
	fmt.Fprintf(o.Out, "服务：    %s\n", strings.TrimSpace(active))
	fmt.Fprintf(o.Out, "面板：    %s\n", orUnknown(env["VPSMON_SERVER"]))
	fmt.Fprintf(o.Out, "节点：    %s（ID %s）\n", orUnknown(env["VPSMON_NODE_NAME"]), orUnknown(env["VPSMON_SERVER_ID"]))
	st, err := ReadStatus(p.statusFile())
	if err != nil {
		fmt.Fprintln(o.Out, "上报：    尚无记录")
		return nil
	}
	fmt.Fprintf(o.Out, "版本：    %s\n", st.Version)
	if st.LastSuccess > 0 {
		fmt.Fprintf(o.Out, "最近成功：%s（%s 前）\n", time.Unix(st.LastSuccess, 0).Format("2006-01-02 15:04:05"),
			time.Since(time.Unix(st.LastSuccess, 0)).Round(time.Second))
	} else {
		fmt.Fprintln(o.Out, "最近成功：从未成功")
	}
	if st.LastError != "" {
		fmt.Fprintf(o.Out, "最近错误：%s（%s）\n", st.LastError, time.Unix(st.LastAttempt, 0).Format("15:04:05"))
		if strings.Contains(st.LastError, "401") {
			fmt.Fprintln(o.Out, "          凭证已失效，请在面板中重新生成注册码后重新安装（设计 43.5）")
		}
	}
	return nil
}

// Uninstall 执行 vpsmon-agent uninstall（设计 27.11）：停止服务，通知面板（节点回到“待安装”并吊销 Token），
// 删除文件与用户。面板不可达时仍完成本地卸载。
func Uninstall(ctx context.Context, o Options) error {
	o.defaults()
	p := o.Paths
	say := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }
	if !o.Sys.IsRoot() {
		return errors.New("需要 root 权限，请使用 sudo 执行")
	}

	o.Sys.Run("systemctl", "disable", "--now", serviceName)
	say("✓ 已停止服务")

	env := readEnv(p.envFile())
	if tok, err := os.ReadFile(p.tokenFile()); err == nil && env["VPSMON_SERVER"] != "" {
		if err := unregister(ctx, o.HTTP, env["VPSMON_SERVER"], strings.TrimSpace(string(tok))); err != nil {
			say("! 未能通知面板（%v），请在面板中手动处理该节点", err)
		} else {
			say("✓ 已通知面板，节点回到“待安装”")
		}
	}

	for _, f := range []string{p.Unit, p.Bin} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			say("! 删除 %s 失败：%v", f, err)
		}
	}
	o.Sys.Run("systemctl", "daemon-reload")
	for _, d := range []string{p.ConfDir, p.StateDir} {
		if err := os.RemoveAll(d); err != nil {
			say("! 删除 %s 失败：%v", d, err)
		}
	}
	if o.Sys.UserExists(userName) {
		o.Sys.Run("userdel", userName)
	}
	say("✓ 已删除程序、配置与用户 %s", userName)
	return nil
}

// unregister 调用 POST /api/v1/agent/unregister（设计 19.10）。超时 5 秒，失败不阻止本地卸载。
func unregister(ctx context.Context, c *http.Client, server, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(server, "/")+"/api/v1/agent/unregister", nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("面板返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

// realSystem 是 Linux 主机上的真实实现。
type realSystem struct{}

func (realSystem) IsRoot() bool { return os.Geteuid() == 0 }

// HasSystemd 按 systemd 官方建议的方式判断：/run/systemd/system 存在（设计 27.5.4）。
func (realSystem) HasSystemd() bool {
	fi, err := os.Stat("/run/systemd/system")
	return err == nil && fi.IsDir()
}

func (realSystem) UserExists(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

func (realSystem) IDs(name string) (int, int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid, nil
}

func (realSystem) Chown(path string, uid, gid int) error { return os.Chown(path, uid, gid) }

func (realSystem) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
