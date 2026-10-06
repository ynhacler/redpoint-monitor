package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"vpsmon/internal/release"
)

// 远程升级的 Agent 侧（设计 29.4、29.13）：Agent 以非 root 运行，只负责
//  1. 定期向面板查询升级任务（GET /api/v1/agent/upgrade）
//  2. 校验面板转交的官方签名清单，下载并校验本机构建
//  3. 把文件与 request.json 写入暂存目录，由 systemd path 单元触发特权 updater
//  4. 把 updater 写下的 result.json 上报给面板
// Agent 没有权限替换程序或重启服务；即使 Agent 被攻破，updater 也只会安装官方签名、版本更高的版本。

// Request 是 Agent 交给 updater 的升级请求（updater 视为不可信输入）。
type Request struct {
	TaskID  int64  `json:"task_id"`
	Version string `json:"version"`
}

// Result 是 updater 的执行结果，由 Agent 上报。
type Result struct {
	TaskID int64  `json:"task_id"`
	Status string `json:"status"` // success / failed / rolled_back
	Reason string `json:"reason,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// RemoteOptions 是 Agent 侧检查升级任务的参数。
type RemoteOptions struct {
	Server    string // 面板地址
	Token     string // Agent Token
	Current   string // 当前版本
	Keys      []release.PublicKey
	StageDir  string // /var/lib/vpsmon-agent/update（Agent 可写；updater 只读其中的文件并复验）
	Downloads string // 下载构建的地址，默认官方 GitHub Releases
	HTTP      *http.Client
	Log       func(format string, a ...any)
}

// CheckRemote 执行一轮：先上报上次的结果，再查询并暂存新任务。错误只影响本轮。
func CheckRemote(ctx context.Context, o RemoteOptions) error {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	if o.Downloads == "" {
		o.Downloads = OfficialReleases
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if err := os.MkdirAll(o.StageDir, 0o750); err != nil {
		return fmt.Errorf("无法创建暂存目录 %s：%w", o.StageDir, err)
	}
	o.reportResult(ctx)

	var task struct {
		Upgrade   bool   `json:"upgrade"`
		TaskID    int64  `json:"task_id"`
		Version   string `json:"version"`
		Manifest  []byte `json:"manifest"`
		Signature string `json:"manifest_signature"`
		Mirror    string `json:"mirror_path"` // 可选：面板镜像路径，如 /releases（设计 27.5.3）
	}
	if err := o.api(ctx, http.MethodGet, "/api/v1/agent/upgrade?current_version="+o.Current+"&os="+runtime.GOOS+"&arch="+ArchLabel(), nil, &task); err != nil {
		if errors.Is(err, errGone) {
			return nil // 面板版本较旧，没有升级接口：不算错误，不写日志
		}
		return fmt.Errorf("查询升级任务失败：%w", err)
	}
	if !task.Upgrade {
		return nil
	}
	if req, err := readRequest(o.StageDir); err == nil && req.TaskID == task.TaskID {
		return nil // 已暂存，等待 updater
	}
	if r, ok := readResult(o.StageDir); ok && r.TaskID == task.TaskID {
		return nil // updater 已执行，结果尚未送达面板，下一轮重试上报
	}
	if err := checkStageWritable(o.StageDir); err != nil {
		o.Log("upgrade task %d failed: %v", task.TaskID, err)
		o.status(ctx, task.TaskID, "failed", err.Error())
		return err
	}
	if err := o.stage(ctx, task.TaskID, task.Version, task.Manifest, []byte(task.Signature), task.Mirror); err != nil {
		o.Log("upgrade task %d failed: %v", task.TaskID, err)
		o.status(ctx, task.TaskID, "failed", err.Error())
		return err
	}
	o.Log("upgrade task %d staged: %s → %s", task.TaskID, o.Current, task.Version)
	o.status(ctx, task.TaskID, "staged", "")
	return nil
}

// stage 校验清单并下载本机构建，全部通过后写入 request.json（最后一步，原子替换）。
func (o *RemoteOptions) stage(ctx context.Context, taskID int64, version string, manifest, sig []byte, mirror string) error {
	m, err := release.VerifyManifest(o.Keys, manifest, sig)
	if err != nil {
		return err
	}
	want, err := release.ParseVersion(version)
	if err != nil {
		return err
	}
	if got, _ := release.ParseVersion(m.Version); got.Compare(want) != 0 {
		return fmt.Errorf("签名清单的版本 %s 与任务 %s 不一致", m.Version, version)
	}
	if err := m.CheckUpgrade(o.Current, false); err != nil {
		return err
	}
	art, ok := m.ArtifactFor(runtime.GOOS, ArchLabel())
	if !ok {
		return fmt.Errorf("版本 %s 没有 %s/%s 的构建", m.Version, runtime.GOOS, ArchLabel())
	}
	cleanStage(o.StageDir)
	dl := &Options{HTTP: o.HTTP, Out: io.Discard}
	part := filepath.Join(o.StageDir, art.File+".part")
	url := strings.TrimRight(o.Downloads, "/") + "/download/v" + m.Version + "/" + art.File
	// 面板已镜像时从本面板下载：只接受以 / 开头的路径，拼在 Agent 自己配置的面板地址后面，
	// 不会被引导到其他主机；下载内容仍按签名清单校验
	if strings.HasPrefix(mirror, "/") && !strings.HasPrefix(mirror, "//") {
		url = strings.TrimRight(o.Server, "/") + mirror + "/v" + m.Version + "/" + art.File
	}
	if err := dl.download(ctx, url, part, art.Size); err != nil {
		os.Remove(part)
		return err
	}
	if got, err := fileSHA256(part); err != nil || got != art.SHA256 {
		os.Remove(part)
		return fmt.Errorf("SHA256 校验失败（期望 %s，实际 %s）", art.SHA256, got)
	}
	if err := os.Rename(part, filepath.Join(o.StageDir, art.File)); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.StageDir, "manifest.json"), manifest, 0o640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.StageDir, "manifest.json.minisig"), sig, 0o640); err != nil {
		return err
	}
	b, _ := json.Marshal(Request{TaskID: taskID, Version: m.Version})
	tmp := filepath.Join(o.StageDir, "request.json.tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(o.StageDir, "request.json")) // 触发 updater（systemd path 单元，或 OpenRC 的定时检查）
}

// reportResult 上报 updater 留下的结果，成功送达（或任务已不存在）后删除。
func (o *RemoteOptions) reportResult(ctx context.Context) {
	r, ok := readResult(o.StageDir)
	if !ok {
		return
	}
	reason := r.Reason
	if r.Status == "success" {
		reason = ""
	}
	if err := o.status(ctx, r.TaskID, r.Status, reason); err == nil || errors.Is(err, errGone) {
		os.Remove(filepath.Join(o.StageDir, "result.json"))
		o.Log("upgrade task %d result reported: %s", r.TaskID, r.Status)
	}
}

var errGone = errors.New("task gone")

func (o *RemoteOptions) status(ctx context.Context, taskID int64, status, reason string) error {
	return o.api(ctx, http.MethodPost, "/api/v1/agent/upgrade/status",
		map[string]any{"task_id": taskID, "status": status, "reason": reason}, nil)
}

func (o *RemoteOptions) api(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.Server, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errGone
	case resp.StatusCode == http.StatusNoContent:
		return nil
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s %s 返回 %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// readResult 读取 updater 的结果。内容无效时：刚写下的可能尚未写完，留到下一轮；超过 10 分钟仍无效则删除。
func readResult(dir string) (Result, bool) {
	path := filepath.Join(dir, "result.json")
	var r Result
	b, err := readLimited(path, 8<<10)
	if err != nil {
		return r, false
	}
	if json.Unmarshal(b, &r) != nil || r.TaskID == 0 {
		if fi, err := os.Lstat(path); err == nil && time.Since(fi.ModTime()) > 10*time.Minute {
			os.Remove(path)
		}
		return r, false
	}
	return r, true
}

func readRequest(dir string) (*Request, error) {
	b, err := readLimited(filepath.Join(dir, "request.json"), 4<<10)
	if err != nil {
		return nil, err
	}
	var r Request
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// StageFixHint 是暂存目录不可写时的修复方法（在主机上以 root 执行，会把目录属主改回 Agent 用户）。
const StageFixHint = "在主机上执行 sudo vpsmon-agent enable-remote-upgrade 修复"

// checkStageWritable 确认 Agent 能在暂存目录中创建文件。目录属主不对时（例如曾以 root 运行过 Agent、
// 或旧版本留下 root 所有的目录）说明属主与修复方法，而不是只报 permission denied。
func checkStageWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".probe-")
	if err != nil {
		owner := ""
		if fi, e := os.Stat(dir); e == nil {
			if uid, ok := fileOwner(fi); ok && uid != os.Getuid() {
				owner = fmt.Sprintf("（属主 uid %d，Agent 以 uid %d 运行）", uid, os.Getuid())
			}
		}
		return fmt.Errorf("暂存目录 %s 不可写%s：%s", dir, owner, StageFixHint)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// cleanStage 删除暂存目录中上一轮的文件（保留 result.json，等待上报）。
func cleanStage(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "result.json" {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
