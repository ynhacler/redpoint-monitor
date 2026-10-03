package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"vpsmon/internal/release"
)

// 官方 Agent 版本同步（设计 27.3.1、29.1）。
//
// 面板定期从官方发布地址获取最新的签名清单，用编译进面板的官方公钥验签；只有验签通过的版本才会被记录，
// 并用于生成安装命令（脚本的 SHA256 来自已验签的清单）。
// 【安全】面板只“选择”官方版本，不“制造”版本：不持有私钥，不接受上传，不修改清单（CLAUDE.md 约束 2、9）。

const (
	releaseSyncInterval = 6 * time.Hour
	maxManifestBytes    = 64 << 10
	maxSignatureBytes   = 4 << 10
)

// OfficialReleases 是官方发布地址（公开的 GitHub Releases）。
const OfficialReleases = "https://github.com/ynhacler/redpoint-monitor/releases"

// agentRelease 是一个已验签的官方版本。
type agentRelease struct {
	Version         string             `json:"version"`
	Channel         string             `json:"channel"`
	KeyID           string             `json:"key_id"`
	InstallerFile   string             `json:"installer_file"`
	InstallerSHA256 string             `json:"installer_sha256"`
	ReleasedAt      string             `json:"released_at"`
	SyncedAt        int64              `json:"synced_at"`
	Notes           string             `json:"notes,omitempty"`
	Artifacts       []release.Artifact `json:"artifacts"`
	Mirrored        bool               `json:"mirrored"` // 全部文件已校验并保存在本面板（设计 27.5.3）
}

// SaveRelease 记录一个已验签的版本；已存在时更新同步时间（清单内容相同才会验签通过）。
func (s *Store) SaveRelease(m *release.Manifest, manifest, sig []byte, keyID string, now time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO agent_releases (version, channel, manifest, signature, key_id, installer_file,
		installer_sha256, released_at, synced_at) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(version) DO UPDATE SET synced_at = excluded.synced_at`,
		m.Version, m.Channel, manifest, string(sig), keyID, m.Installer.File, m.Installer.SHA256, m.ReleasedAt, now.Unix())
	return err
}

// ListReleases 返回已同步的版本，最新在前（按语义化版本排序）。
// 每次读取时重新验签清单原文：数据库被改动的版本不会被使用。
func (s *Store) ListReleases(keys []release.PublicKey) ([]agentRelease, error) {
	rows, err := s.DB.Query(`SELECT manifest, signature, key_id, synced_at, mirrored_at FROM agent_releases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentRelease{}
	for rows.Next() {
		var data []byte
		var sig, keyID string
		var synced, mirrored int64
		if err := rows.Scan(&data, &sig, &keyID, &synced, &mirrored); err != nil {
			return nil, err
		}
		m, err := release.VerifyManifest(keys, data, []byte(sig))
		if err != nil {
			continue
		}
		out = append(out, agentRelease{Version: m.Version, Channel: m.Channel, KeyID: keyID,
			InstallerFile: m.Installer.File, InstallerSHA256: m.Installer.SHA256, ReleasedAt: m.ReleasedAt,
			SyncedAt: synced, Notes: m.Notes, Artifacts: m.Artifacts, Mirrored: mirrored > 0})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortReleases(out)
	return out, nil
}

func sortReleases(list []agentRelease) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0; j-- {
			a, _ := release.ParseVersion(list[j].Version)
			b, _ := release.ParseVersion(list[j-1].Version)
			if a.Compare(b) <= 0 {
				break
			}
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// latestStable 返回最新的已验签正式版；没有时返回 nil。
func (s *Server) latestStable() *agentRelease {
	list, err := s.store.ListReleases(s.releaseKeys)
	if err != nil {
		s.log.Error("list releases failed", "component", "release", "err", err)
		return nil
	}
	for i := range list {
		if list[i].Channel == "stable" {
			return &list[i]
		}
	}
	return nil
}

// syncReleases 从官方地址获取最新的签名清单并验签，通过后记录。返回同步到的版本。
func (s *Server) syncReleases(ctx context.Context) (*release.Manifest, error) {
	base := strings.TrimRight(s.releaseBase, "/") + "/latest/download"
	data, err := s.fetchLimited(ctx, base+"/manifest.json", maxManifestBytes)
	if err != nil {
		return nil, err
	}
	sig, err := s.fetchLimited(ctx, base+"/manifest.json.minisig", maxSignatureBytes)
	if err != nil {
		return nil, err
	}
	m, err := release.VerifyManifest(s.releaseKeys, data, sig)
	if err != nil {
		// 【安全】验签失败的版本不会被记录，也不会出现在安装命令中（设计 27.5.5）
		return nil, fmt.Errorf("官方版本验签失败：%w", err)
	}
	if err := s.store.SaveRelease(m, data, sig, keyIDOf(s.releaseKeys, sig), time.Now()); err != nil {
		return nil, err
	}
	// --release-mirror：同时把该版本的全部文件镜像到本面板（设计 27.5.3）
	if s.releaseMirror {
		if err := s.mirrorRelease(ctx, m, data, sig); err != nil {
			return m, fmt.Errorf("已同步 %s，但镜像失败：%w", m.Version, err)
		}
	}
	return m, nil
}

func (s *Server) fetchLimited(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.releaseHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法访问官方发布地址：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("官方发布地址返回 %d：%s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("官方发布文件超过大小上限")
	}
	return b, nil
}

// releaseSyncLoop 启动 30 秒后同步一次，之后每 6 小时一次（设计 29.1）。失败只记录日志。
func (s *Server) releaseSyncLoop(ctx context.Context) {
	wait := 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = releaseSyncInterval
		if m, err := s.syncReleases(ctx); err != nil {
			s.log.Warn("release sync failed", "component", "release", "err", err)
		} else {
			s.log.Info("release synced", "component", "release", "version", m.Version, "channel", m.Channel)
		}
	}
}

// handleReleases：GET /api/v1/agent-releases，admin。已同步并验签的官方版本，最新在前（设计 29.20）。
func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListReleases(s.releaseKeys)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, map[string]any{"items": list, "auto_sync": !s.noReleaseSync, "source": s.releaseBase, "mirror": s.releaseMirror})
}

// handleSyncReleases：POST /api/v1/agent-releases/sync，admin。立即从官方地址同步并验签（设计 29.20）。
// 成功返回同步到的版本；无法访问或验签失败返回 503（unavailable）并说明原因。
func (s *Server) handleSyncReleases(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	m, err := s.syncReleases(ctx)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "release.sync", Success: err == nil, Details: map[string]any{
		"version": versionOf(m), "error": errString(err)}})
	if err != nil {
		s.log.Warn("release sync failed", "component", "release", "err", err)
		s.writeError(w, r, &APIError{Code: CodeUnavailable, Message: err.Error()})
		return
	}
	writeJSON(w, map[string]any{"version": m.Version, "channel": m.Channel})
}

func versionOf(m *release.Manifest) string {
	if m == nil {
		return ""
	}
	return m.Version
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
