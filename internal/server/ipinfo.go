package server

// 节点 IP 信息（设计 49，修订第 191 条）：用 ipinfo.io 查询各节点公网 IP 的 ASN、运营方与位置，显示在节点详情中。
// 【隐私】会把节点的公网 IP 发给第三方 ipinfo.io，按设计 1.8（IP 上传默认关闭）默认关闭，管理员在设置中打开后才查询；
// 关闭时不发送任何 IP。面板直接请求 https://ipinfo.io/<IP>/json（不带令牌，免费额度），每个节点每周一次，
// 地址变化时立即重新查询；失败的一天后再试。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	settingIPInfo   = "ipinfo"
	ipinfoEvery     = 7 * 24 * time.Hour
	ipinfoRetry     = 24 * time.Hour
	ipinfoPerRound  = 30
	ipinfoUserAgent = "vpsmon-panel"
)

// IPInfoSetting 是查询开关（api/openapi.yaml IPInfoSetting）。
type IPInfoSetting struct {
	Enabled bool `json:"enabled"`
}

// IPInfo 是一个节点的查询结果（api/openapi.yaml IPInfo）。
type IPInfo struct {
	IP        string `json:"ip"`
	ASN       string `json:"asn"`
	Org       string `json:"org"`
	Hostname  string `json:"hostname"`
	City      string `json:"city"`
	Region    string `json:"region"`
	Country   string `json:"country"`
	Timezone  string `json:"timezone"`
	FetchedAt int64  `json:"fetched_at"`
	Error     string `json:"error"`
}

// ---- 存储 ----

func (s *Store) ipinfoEnabled() bool {
	var v IPInfoSetting
	_, _ = s.GetSetting(settingIPInfo, &v)
	return v.Enabled
}

// GetIPInfo 读取节点的查询结果；没有时返回 nil。
func (s *Store) GetIPInfo(serverID int64) (*IPInfo, error) {
	var x IPInfo
	err := s.DB.QueryRow(`SELECT ip, asn, org, hostname, city, region, country, timezone, fetched_at, error FROM server_ipinfo
		WHERE server_id = ?`, serverID).Scan(&x.IP, &x.ASN, &x.Org, &x.Hostname, &x.City, &x.Region, &x.Country, &x.Timezone, &x.FetchedAt, &x.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// SaveIPInfo 保存查询结果；失败时只更新错误与时间，保留上次成功的信息。
func (s *Store) SaveIPInfo(serverID int64, x IPInfo) error {
	if x.Error != "" {
		_, err := s.DB.Exec(`INSERT INTO server_ipinfo (server_id, ip, fetched_at, error) VALUES (?,?,?,?)
			ON CONFLICT (server_id) DO UPDATE SET fetched_at = excluded.fetched_at, error = excluded.error,
			ip = CASE WHEN server_ipinfo.asn = '' THEN excluded.ip ELSE server_ipinfo.ip END`, serverID, x.IP, x.FetchedAt, x.Error)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO server_ipinfo (server_id, ip, asn, org, hostname, city, region, country, timezone, fetched_at, error)
		VALUES (?,?,?,?,?,?,?,?,?,?,'') ON CONFLICT (server_id) DO UPDATE SET ip = excluded.ip, asn = excluded.asn, org = excluded.org,
		hostname = excluded.hostname, city = excluded.city, region = excluded.region, country = excluded.country,
		timezone = excluded.timezone, fetched_at = excluded.fetched_at, error = ''`,
		serverID, x.IP, x.ASN, x.Org, x.Hostname, x.City, x.Region, x.Country, x.Timezone, x.FetchedAt)
	return err
}

// ---- 查询 ----

// ipinfoDue 判断节点是否需要查询：没有记录、地址变了、成功超过一周或失败超过一天。
func ipinfoDue(old *IPInfo, ip string, now time.Time) bool {
	if old == nil || old.IP != ip {
		return true
	}
	age := now.Sub(time.Unix(old.FetchedAt, 0))
	if old.Error != "" {
		return age >= ipinfoRetry
	}
	return age >= ipinfoEvery
}

// lookupIPInfo 查询一个地址。org 形如“AS13335 Cloudflare, Inc.”，拆成 ASN 与名称。
func (s *Server) lookupIPInfo(ctx context.Context, ip string, now time.Time) IPInfo {
	out := IPInfo{IP: ip, FetchedAt: now.Unix()}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", s.ipinfoBase+"/"+ip+"/json", nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", ipinfoUserAgent)
	res, err := s.ipinfoHTTP.Do(req)
	if err != nil {
		out.Error = "无法连接 ipinfo.io"
		return out
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		out.Error = fmt.Sprintf("ipinfo.io 返回 HTTP %d", res.StatusCode)
		if res.StatusCode == http.StatusTooManyRequests {
			out.Error = "ipinfo.io 免费额度已用完（HTTP 429），稍后再试"
		}
		return out
	}
	var v struct {
		IP, Hostname, City, Region, Country, Org, Timezone string
		Bogon                                              bool
	}
	if err := json.Unmarshal(b, &v); err != nil {
		out.Error = "ipinfo.io 返回的内容无法解析"
		return out
	}
	if v.Bogon {
		out.Error = "不是公网地址"
		return out
	}
	out.Hostname, out.City, out.Region, out.Country, out.Timezone = v.Hostname, v.City, v.Region, v.Country, v.Timezone
	out.Org = strings.TrimSpace(v.Org)
	if asn, name, ok := strings.Cut(out.Org, " "); ok && strings.HasPrefix(asn, "AS") {
		out.ASN, out.Org = asn, strings.TrimSpace(name)
	}
	return out
}

// ipinfoLoop 每小时检查一次需要查询的节点（打开开关后才查询）。
func (s *Server) ipinfoLoop(ctx context.Context) {
	wait := 2 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		s.ipinfoRound(ctx, time.Now())
	}
}

// ipinfoRound 查询到期的节点，每轮最多 30 个、间隔 1 秒（免费额度按天计）。
func (s *Server) ipinfoRound(ctx context.Context, now time.Time) {
	if !s.store.ipinfoEnabled() {
		return
	}
	rows, err := s.store.ListServers()
	if err != nil {
		return
	}
	n := 0
	for _, row := range rows {
		ip, ok := exposureTarget(row)
		if !ok {
			continue
		}
		old, err := s.store.GetIPInfo(row.ID)
		if err != nil || !ipinfoDue(old, ip, now) {
			continue
		}
		if n >= ipinfoPerRound || ctx.Err() != nil {
			return
		}
		if n > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		n++
		x := s.lookupIPInfo(ctx, ip, now)
		if x.Error != "" {
			s.log.Warn("ipinfo lookup failed", "component", "ipinfo", "server_id", row.ID, "err", x.Error)
		}
		if err := s.store.SaveIPInfo(row.ID, x); err != nil {
			s.log.Error("save ipinfo failed", "component", "ipinfo", "err", err)
		}
	}
}

// ---- 接口（Web 管理员） ----

// handleGetIPInfoSetting：GET /api/v1/settings/ipinfo，admin。
func (s *Server) handleGetIPInfoSetting(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, IPInfoSetting{Enabled: s.store.ipinfoEnabled()})
}

// handlePutIPInfoSetting：PUT /api/v1/settings/ipinfo，admin。打开时立即在后台查询一轮。
func (s *Server) handlePutIPInfoSetting(w http.ResponseWriter, r *http.Request) {
	var b IPInfoSetting
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.PutSetting(settingIPInfo, b, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true, Details: map[string]any{"key": settingIPInfo, "enabled": b.Enabled}})
	if b.Enabled {
		go s.ipinfoRound(context.Background(), time.Now())
	}
	writeJSON(w, b)
}

// handleRefreshIPInfo：POST /api/v1/servers/{id}/ipinfo，admin。立即查询一次。
func (s *Server) handleRefreshIPInfo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.store.ipinfoEnabled() {
		s.writeError(w, r, errorf(CodeConflict, "没有打开 IP 信息查询（系统 → 监测）"))
		return
	}
	ip, ok := exposureTarget(*row)
	if !ok {
		s.writeError(w, r, errorf(CodeValidationFailed, "节点没有公网 IP"))
		return
	}
	x := s.lookupIPInfo(r.Context(), ip, time.Now())
	if err := s.store.SaveIPInfo(id, x); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	saved, err := s.store.GetIPInfo(id)
	if err != nil || saved == nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, saved)
}
