// Package setup 实现 vpsmon-agent 的本地管理命令：install、status、uninstall（设计 27.6.1、27.11）。
//
// 安装与注册逻辑写在 Agent 二进制中，安装脚本只负责下载与校验（设计 27.3.3、27.5.1）。
// 这些命令由主机管理员以 root 在本机执行；【安全】Agent 运行时只采集与上报，
// 从不执行面板下发的任何命令（设计 1.6.8）。
//
// 不负责：下载与校验二进制（agent.sh，设计 27.5）、升级（设计 29）、非 systemd 系统（TODO(N1)，设计 27.12）。
package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// unitFile 是 systemd 单元，与 deploy/systemd/vpsmon-agent.service 保持一致（由测试保证）。
//
//go:embed vpsmon-agent.service
var unitFile string

// 远程升级的 systemd 单元（设计 29.13），同样与 deploy/systemd 中的保持一致。
//
//go:embed vpsmon-agent-updater.path
var updaterPathFile string

//go:embed vpsmon-agent-updater.service
var updaterServiceFile string

const (
	userName    = "vpsmon-agent" // 运行 Agent 的系统用户（设计 1.6.9）
	serviceName = "vpsmon-agent"
)

// Paths 是 Agent 在主机上的文件布局（设计 27.10）。测试中指向临时目录。
type Paths struct {
	Bin      string // /usr/local/bin/vpsmon-agent
	ConfDir  string // /etc/vpsmon-agent：token 与 env
	StateDir string // /var/lib/vpsmon-agent：status.json，由 systemd StateDirectory 创建
	Unit     string // /etc/systemd/system/vpsmon-agent.service
	// 远程升级（设计 29.13）
	Updater        string // /usr/local/lib/vpsmon-agent/updater：updater 的独立副本，不随远程升级替换
	UpdaterPath    string // /etc/systemd/system/vpsmon-agent-updater.path
	UpdaterService string // /etc/systemd/system/vpsmon-agent-updater.service
}

// DefaultPaths 是 Linux 主机上的默认路径。
var DefaultPaths = Paths{
	Bin:      "/usr/local/bin/vpsmon-agent",
	ConfDir:  "/etc/vpsmon-agent",
	StateDir: "/var/lib/vpsmon-agent",
	Unit:     "/etc/systemd/system/vpsmon-agent.service",

	Updater:        "/usr/local/lib/vpsmon-agent/updater",
	UpdaterPath:    "/etc/systemd/system/vpsmon-agent-updater.path",
	UpdaterService: "/etc/systemd/system/vpsmon-agent-updater.service",
}

func (p Paths) tokenFile() string  { return filepath.Join(p.ConfDir, "token") }
func (p Paths) envFile() string    { return filepath.Join(p.ConfDir, "env") }
func (p Paths) statusFile() string { return filepath.Join(p.StateDir, "status.json") }

// NoRemoteUpgradeFile 存在时 updater 拒绝远程升级（设计 29.13）。由主机管理员控制，面板无法改变。
func (p Paths) NoRemoteUpgradeFile() string { return filepath.Join(p.ConfDir, "no-remote-upgrade") }

// System 抽象安装过程中对主机的修改，便于在非 Linux 环境中测试完整流程。
type System interface {
	IsRoot() bool
	HasSystemd() bool
	UserExists(name string) bool
	// IDs 返回用户的 uid 与 gid。
	IDs(name string) (uid, gid int, err error)
	Chown(path string, uid, gid int) error
	// Run 执行本机管理命令（useradd、systemctl 等），返回合并后的输出。
	Run(name string, args ...string) (string, error)
}

// Options 是 install / uninstall 的参数。
type Options struct {
	Server          string // 面板地址，如 https://monitor.example.com
	EnrollCode      string
	AllowHTTP       bool          // 仅本地开发：允许向非回环地址明文注册（与 --allow-http 一致）
	NoRemoteUpgrade bool          // 不启用远程升级（设计 29.13）；之后可用 enable-remote-upgrade 打开
	Version         string        // 本程序版本，注册时上报
	Self            string        // 当前可执行文件路径；不在 Paths.Bin 时复制过去
	Paths           Paths         // 为空时使用 DefaultPaths
	Sys             System        // 为空时使用真实系统
	HTTP            *http.Client  // 为空时使用默认客户端（10 秒超时，始终校验证书）
	Out             io.Writer     // 进度输出
	WaitFirst       time.Duration // 等待首次上报成功的时长，默认 20 秒
	HostInfoRoot    string        // 读取 /etc/hostname 等文件的根目录，测试用；默认 "/"
}

func (o *Options) defaults() {
	if o.Paths == (Paths{}) {
		o.Paths = DefaultPaths
	}
	if o.Sys == nil {
		o.Sys = realSystem{}
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.WaitFirst == 0 {
		o.WaitFirst = 20 * time.Second
	}
	if o.HostInfoRoot == "" {
		o.HostInfoRoot = "/"
	}
}

// ValidateServerURL 校验面板地址，强制 HTTPS（设计 23.1）。
//
// 【安全】每个请求都携带凭证，公网明文 HTTP 会泄露 Token；回环地址的流量不离开本机，允许 HTTP；
// allowHTTP 仅用于本地开发。
func ValidateServerURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid --server %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" || allowHTTP {
			return nil
		}
		return errors.New("refusing plain HTTP to a remote server; use HTTPS (or --allow-http for local dev)")
	}
	return fmt.Errorf("unsupported scheme %q", u.Scheme)
}

// enrollCodePattern 是注册码格式，与面板一致（设计 27.4）；本地先检查，给出更友好的提示。
var enrollCodePattern = regexp.MustCompile(`^ENR-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$`)

// Install 执行 vpsmon-agent install（设计 27.6.1）。
//
// 顺序与设计略有不同：先完成预检与注册，再修改主机。注册码无效时主机上不留下任何东西；
// 注册之后的步骤失败时回滚已创建的用户、目录、服务单元（设计 43.8），
// 并提示 10 分钟内可用同一注册码重试（设计 27.6.4）。
func Install(ctx context.Context, o Options) error {
	o.defaults()
	p := o.Paths
	say := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }

	// 1. 预检
	if !o.Sys.IsRoot() {
		return errors.New("需要 root 权限，请使用 sudo 执行")
	}
	if !o.Sys.HasSystemd() {
		return errors.New("未检测到 systemd：目前只支持 systemd 系统（OpenRC 等见设计 27.12，尚未支持）")
	}
	o.Server = strings.TrimRight(strings.TrimSpace(o.Server), "/")
	if err := ValidateServerURL(o.Server, o.AllowHTTP); err != nil {
		return err
	}
	code := strings.ToUpper(strings.TrimSpace(o.EnrollCode))
	if !enrollCodePattern.MatchString(code) {
		return errors.New("注册码格式不正确，应为 ENR-XXXX-XXXX-XXXX-XXXX，请从面板复制完整命令")
	}
	if _, err := os.Stat(p.tokenFile()); err == nil {
		return fmt.Errorf("本机已安装 Agent（%s 已存在）。重装请先执行 sudo vpsmon-agent uninstall", p.tokenFile())
	}

	// 2. 采集主机信息并注册
	host := readHostInfo(o.HostInfoRoot)
	say("✓ 系统 %s %s · linux/%s", orUnknown(host.OS), host.OSVersion, host.Arch)
	res, err := enroll(ctx, o.HTTP, o.Server, enrollRequest{EnrollCode: code, Hostname: host.Hostname,
		MachineIDHash: host.MachineIDHash, OS: host.OS, OSVersion: host.OSVersion, Arch: host.Arch, AgentVersion: o.Version})
	if err != nil {
		return err
	}
	say("✓ 已注册到 %s ，节点：%s", o.Server, res.ServerName)
	for _, w := range res.Warnings {
		say("  ! %s", w)
	}

	// 3. 修改主机；任何一步失败都回滚已做的修改
	var undo []func()
	rollback := func(cause error) error {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		return fmt.Errorf("%w\n已回滚本次安装。注册码在 10 分钟内仍可在本机重试同一命令", cause)
	}
	if !o.Sys.UserExists(userName) {
		if out, err := o.Sys.Run("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", userName); err != nil {
			return rollback(fmt.Errorf("创建用户 %s 失败：%v %s", userName, err, out))
		}
		undo = append(undo, func() { o.Sys.Run("userdel", userName) })
	}
	_, gid, err := o.Sys.IDs(userName)
	if err != nil {
		return rollback(err)
	}
	if err := os.MkdirAll(p.ConfDir, 0o750); err != nil {
		return rollback(err)
	}
	undo = append(undo, func() { os.RemoveAll(p.ConfDir) })
	if err := o.Sys.Chown(p.ConfDir, 0, gid); err != nil {
		return rollback(err)
	}
	// 【安全】Token 文件 root:vpsmon-agent 0640：只有 Agent 进程与 root 可读（设计 27.6.1、27.10）
	if err := writeFile(o.Sys, p.tokenFile(), res.AgentToken+"\n", 0o640, gid); err != nil {
		return rollback(err)
	}
	env := fmt.Sprintf("# 由 vpsmon-agent install 生成（设计 27.10）\nVPSMON_SERVER=%s\nVPSMON_SERVER_ID=%d\nVPSMON_NODE_NAME=%s\n",
		o.Server, res.ServerID, shellSafe(res.ServerName))
	if err := writeFile(o.Sys, p.envFile(), env, 0o640, gid); err != nil {
		return rollback(err)
	}
	if o.Self != "" && o.Self != p.Bin {
		if err := copyFile(o.Self, p.Bin, 0o755); err != nil {
			return rollback(fmt.Errorf("安装二进制到 %s 失败：%w", p.Bin, err))
		}
		undo = append(undo, func() { os.Remove(p.Bin) })
	}
	say("✓ 已安装 %s", p.Bin)
	if err := os.WriteFile(p.Unit, []byte(unitFile), 0o644); err != nil {
		return rollback(err)
	}
	undo = append(undo, func() {
		o.Sys.Run("systemctl", "disable", "--now", serviceName)
		os.Remove(p.Unit)
		o.Sys.Run("systemctl", "daemon-reload")
	})
	if out, err := o.Sys.Run("systemctl", "daemon-reload"); err != nil {
		return rollback(fmt.Errorf("systemctl daemon-reload 失败：%v %s", err, out))
	}
	if o.NoRemoteUpgrade {
		if err := writeFile(o.Sys, p.NoRemoteUpgradeFile(), "# 存在时拒绝远程升级（设计 29.13）\n", 0o644, 0); err != nil {
			return rollback(err)
		}
		say("✓ 未启用远程升级（启用：sudo vpsmon-agent enable-remote-upgrade）")
	} else {
		undo = append(undo, func() { removeUpdater(o) })
		if err := installUpdater(o); err != nil {
			return rollback(fmt.Errorf("安装远程升级组件失败：%w", err))
		}
		say("✓ 已启用远程升级：只安装官方签名、版本更高的 Agent（关闭：sudo touch %s）", p.NoRemoteUpgradeFile())
	}
	started := time.Now()
	if out, err := o.Sys.Run("systemctl", "enable", "--now", serviceName); err != nil {
		return rollback(fmt.Errorf("启动服务失败：%v %s", err, out))
	}

	// 4. 等待首次上报（设计 27.6.1 第 8 步）。超时不算失败：服务已安装，可能只是网络慢
	if st, ok := waitFirstReport(p.statusFile(), started, o.WaitFirst); ok {
		say("✓ 服务已启动，首次上报成功")
	} else if st != nil && st.LastError != "" {
		say("! 服务已启动，但上报失败：%s", st.LastError)
		say("  查看日志：journalctl -u %s -n 50", serviceName)
	} else {
		say("! 服务已启动，尚未确认首次上报；稍后用 vpsmon-agent status 查看")
	}
	say("")
	say("查看状态：vpsmon-agent status")
	say("卸载：    sudo vpsmon-agent uninstall")
	return nil
}

// installUpdater 安装或刷新远程升级组件（设计 29.13）：updater 的独立副本与 systemd path / service 单元。
// 【安全】updater 以 root 运行，只从暂存目录读取并用内置公钥复验；它自身只随 install / 本机 upgrade 更新，
// 远程升级只替换 /usr/local/bin/vpsmon-agent，因此被替换的程序无法改变执行替换的程序。
func installUpdater(o Options) error {
	p := o.Paths
	src := o.Self
	if src == "" {
		src = p.Bin
	}
	if err := os.MkdirAll(filepath.Dir(p.Updater), 0o755); err != nil {
		return err
	}
	if err := copyFile(src, p.Updater, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.UpdaterService, []byte(updaterServiceFile), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(p.UpdaterPath, []byte(updaterPathFile), 0o644); err != nil {
		return err
	}
	if out, err := o.Sys.Run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败：%v %s", err, out)
	}
	if out, err := o.Sys.Run("systemctl", "enable", "--now", "vpsmon-agent-updater.path"); err != nil {
		return fmt.Errorf("启用 vpsmon-agent-updater.path 失败：%v %s", err, out)
	}
	return nil
}

func removeUpdater(o Options) {
	p := o.Paths
	o.Sys.Run("systemctl", "disable", "--now", "vpsmon-agent-updater.path")
	for _, f := range []string{p.UpdaterPath, p.UpdaterService, p.Updater} {
		os.Remove(f)
	}
	os.Remove(filepath.Dir(p.Updater))
}

// EnableRemoteUpgrade 为已安装的 Agent 启用远程升级（sudo vpsmon-agent enable-remote-upgrade）。
func EnableRemoteUpgrade(o Options) error {
	o.defaults()
	if !o.Sys.IsRoot() {
		return errors.New("需要 root 权限，请使用 sudo 执行")
	}
	if _, err := os.Stat(o.Paths.tokenFile()); err != nil {
		return errors.New("本机尚未安装 Agent，请先执行面板中的安装命令")
	}
	if err := installUpdater(o); err != nil {
		return err
	}
	if err := os.Remove(o.Paths.NoRemoteUpgradeFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(o.Out, "✓ 已启用远程升级：只安装官方签名、版本更高的 Agent（关闭：sudo touch %s）\n", o.Paths.NoRemoteUpgradeFile())
	return nil
}

// RefreshUpdater 在本机升级后刷新 updater 副本（只在已启用远程升级时）。
func RefreshUpdater(o Options) error {
	o.defaults()
	if _, err := os.Stat(o.Paths.UpdaterPath); err != nil {
		return nil
	}
	return copyFile(o.Paths.Bin, o.Paths.Updater, 0o755)
}

// shellSafe 去掉换行等字符，节点名写入 env 文件时不能破坏格式（systemd EnvironmentFile 按行解析）。
func shellSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '"' || r == '\\' || r == '$' || r == '`' {
			return -1
		}
		return r
	}, s)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// writeFile 原子写入：先写临时文件、设置权限与属组，再改名，避免出现权限过宽的中间状态。
func writeFile(sys System, path, content string, mode os.FileMode, gid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile 受 umask 影响，这里显式设置
		os.Remove(tmp)
		return err
	}
	if err := sys.Chown(tmp, 0, gid); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// HostInfo 是注册时上报的主机信息（设计 27.6.2）。
type HostInfo struct {
	Hostname      string
	MachineIDHash string
	OS, OSVersion string
	Arch          string
}

// readHostInfo 读取主机信息。只上报 machine-id 的哈希，不上报原值（设计 27.6.1）。
func readHostInfo(root string) HostInfo {
	h := HostInfo{Arch: goarch}
	h.Hostname, _ = os.Hostname()
	if b, err := os.ReadFile(filepath.Join(root, "etc/machine-id")); err == nil && len(bytes.TrimSpace(b)) > 0 {
		sum := sha256.Sum256(bytes.TrimSpace(b))
		h.MachineIDHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	if b, err := os.ReadFile(filepath.Join(root, "etc/os-release")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"'`)
			switch k {
			case "ID":
				h.OS = v
			case "VERSION_ID":
				h.OSVersion = v
			}
		}
	}
	return h
}

type enrollRequest struct {
	EnrollCode    string `json:"enroll_code"`
	Hostname      string `json:"hostname"`
	MachineIDHash string `json:"machine_id_hash"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version"`
	Arch          string `json:"arch"`
	AgentVersion  string `json:"agent_version"`
}

type enrollResponse struct {
	ServerID   int64    `json:"server_id"`
	ServerName string   `json:"server_name"`
	AgentToken string   `json:"agent_token"`
	Warnings   []string `json:"warnings"`
}

// apiError 是面板的统一错误响应（设计 43.4）。
type apiError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// enroll 调用 POST /api/v1/agent/enroll，把面板的错误转换为可读的提示（设计 43.9）。
func enroll(ctx context.Context, c *http.Client, server string, req enrollRequest) (*enrollResponse, error) {
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/agent/enroll", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(r)
	if err != nil {
		return nil, fmt.Errorf("无法连接面板 %s：%w", server, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusOK {
		var res enrollResponse
		if err := json.Unmarshal(data, &res); err != nil || !strings.HasPrefix(res.AgentToken, "agt_") {
			return nil, fmt.Errorf("面板返回了无法识别的注册结果（HTTP %d）", resp.StatusCode)
		}
		return &res, nil
	}
	var ae apiError
	_ = json.Unmarshal(data, &ae)
	switch {
	case ae.Error.Code == "enroll_code_invalid":
		return nil, errors.New("注册码无效、已使用或已过期，请在面板的节点详情中重新生成")
	case ae.Error.Code == "rate_limited":
		return nil, fmt.Errorf("注册请求过于频繁，请 %s 秒后再试", orUnknown(resp.Header.Get("Retry-After")))
	case ae.Error.Message != "":
		return nil, fmt.Errorf("注册失败：%s（编号 %s）", ae.Error.Message, ae.Error.RequestID)
	}
	return nil, fmt.Errorf("注册失败：面板返回 HTTP %d，请确认 --server 是面板地址", resp.StatusCode)
}

// waitFirstReport 轮询 status.json，直到出现 started 之后的成功上报或超时。
func waitFirstReport(path string, started time.Time, timeout time.Duration) (*Status, bool) {
	deadline := time.Now().Add(timeout)
	var last *Status
	for {
		if st, err := ReadStatus(path); err == nil {
			last = st
			if st.LastSuccess >= started.Unix() {
				return st, true
			}
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
