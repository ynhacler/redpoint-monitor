package setup

// vpsmon-agent re-enroll（设计 27.11）：已安装的主机换绑到同一面板的其他节点，或换到另一个面板，不需要卸载重装。
//
// 顺序保证中途失败时本机仍能上报：
//  1. 用新注册码向（新）面板注册，取得新节点的 Token —— 失败则什么都不改
//  2. 用旧 Token 通知旧面板注销旧节点（回到“待安装”并吊销旧 Token）；尽力而为，失败只提示
//     新旧是同一面板的同一节点时跳过（面板注册时已吊销该节点的旧 Token）
//  3. 写入新 Token 与面板地址，重启服务，等待首次上报
//
// 【安全】换绑必须凭管理员在目标面板上生成的一次性注册码（约束 3）；Token 只写入 root:vpsmon-agent 0640 的文件，
// 不出现在命令行或输出中（约束 4、9）。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ReEnroll 执行换绑；o.Server 为空表示留在当前面板。
func ReEnroll(ctx context.Context, o Options) error {
	o.defaults()
	p := o.Paths
	say := func(format string, a ...any) { fmt.Fprintf(o.Out, format+"\n", a...) }
	if !o.Sys.IsRoot() {
		return errors.New("需要 root 权限，请使用 sudo 执行")
	}
	oldTok, err := os.ReadFile(p.tokenFile())
	if err != nil {
		return errors.New("本机尚未安装 Agent，请使用面板中的安装命令")
	}
	env := readEnv(p.envFile())
	oldServer := env["VPSMON_SERVER"]
	var oldSID int64
	fmt.Sscan(env["VPSMON_SERVER_ID"], &oldSID)

	server := strings.TrimRight(strings.TrimSpace(o.Server), "/")
	if server == "" {
		server = oldServer
	}
	if server == "" {
		return fmt.Errorf("无法从 %s 读取面板地址，请用 --server 指定", p.envFile())
	}
	if err := ValidateServerURL(server, o.AllowHTTP); err != nil {
		return err
	}
	code := strings.ToUpper(strings.TrimSpace(o.EnrollCode))
	if !enrollCodePattern.MatchString(code) {
		return errors.New("注册码格式不正确，应为 ENR-XXXX-XXXX-XXXX-XXXX，请从面板复制")
	}

	// 1. 向（新）面板注册：不带 server_id，注册码属于哪个节点就绑定哪个节点
	host := readHostInfo(o.HostInfoRoot)
	res, err := enroll(ctx, o.HTTP, server, enrollRequest{EnrollCode: code, Hostname: host.Hostname,
		MachineIDHash: host.MachineIDHash, OS: host.OS, OSVersion: host.OSVersion, Arch: host.Arch, AgentVersion: o.Version})
	if err != nil {
		return err
	}
	sameNode := server == oldServer && res.ServerID == oldSID
	say("✓ 已在 %s 注册为节点：%s", server, res.ServerName)

	// 2. 注销旧节点（尽力而为）
	if !sameNode && oldServer != "" {
		if err := unregister(ctx, o.HTTP, oldServer, strings.TrimSpace(string(oldTok))); err != nil {
			say("! 未能通知原面板注销旧节点（%v），请在原面板中手动删除或处理该节点", err)
		} else {
			say("✓ 原面板中的旧节点已回到“待安装”，旧 Token 已吊销")
		}
	}

	// 3. 写入新 Token 与面板地址，重启
	_, gid, err := o.Sys.IDs(userName)
	if err != nil {
		return err
	}
	if err := writeFile(o.Sys, p.tokenFile(), res.AgentToken+"\n", 0o640, gid); err != nil {
		return err
	}
	newEnv := fmt.Sprintf("# 由 vpsmon-agent re-enroll 生成（设计 27.10、27.11）\nVPSMON_SERVER=%s\nVPSMON_SERVER_ID=%d\nVPSMON_NODE_NAME=%s\n",
		server, res.ServerID, shellSafe(res.ServerName))
	if err := writeFile(o.Sys, p.envFile(), newEnv, 0o640, gid); err != nil {
		return err
	}
	for _, w := range res.Warnings {
		say("! %s", w)
	}
	started := time.Now()
	if err := restartService(o.Sys, installedInit(p)); err != nil {
		return err
	}
	if _, ok := waitFirstReport(p.statusFile(), started, o.WaitFirst); ok {
		say("✓ 服务已重启，已向新节点上报")
	} else {
		say("! 服务已重启，尚未确认上报；稍后用 vpsmon-agent status 查看")
	}
	return nil
}
