package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// Product 是 Agent 发布清单中的产品名。
const Product = "vpsmon-agent"

// Artifact 是发布中的一个文件。
type Artifact struct {
	OS     string `json:"os,omitempty"`   // 仅二进制：linux
	Arch   string `json:"arch,omitempty"` // 仅二进制：amd64 / arm64 / armv7 / armv6 / 386 / riscv64 / mips / mipsle（设计 27.5.4）
	File   string `json:"file"`           // 发布中的文件名，如 vpsmon-agent-linux-amd64
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest 是签名的发布清单（设计 29.7.2）。签的是整份清单，而不是单个字段：
// Agent / 面板据此知道版本、通道、可升级的最低版本，以及每个文件的大小与 SHA256。
type Manifest struct {
	Product           string     `json:"product"`
	Version           string     `json:"version"` // 不带 v，如 0.2.0
	Channel           string     `json:"channel"` // stable / beta
	ReleasedAt        string     `json:"released_at"`
	MinUpgradableFrom string     `json:"min_upgradable_from"`
	Artifacts         []Artifact `json:"artifacts"`
	Installer         Artifact   `json:"installer"` // agent-x.y.z.sh（设计 27.5）
	Notes             string     `json:"notes,omitempty"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VerifyManifest 用受信任的公钥校验清单签名，再解析并检查内容（设计 29.7.2）。
// 签名必须覆盖清单的原始字节：先验签、后解析，解析的永远是已验签的内容。
func VerifyManifest(keys []PublicKey, data, sig []byte) (*Manifest, error) {
	if len(keys) == 0 {
		return nil, errors.New("程序中没有官方发布公钥，无法校验发布（开发构建）")
	}
	if _, err := Verify(keys, data, sig); err != nil {
		return nil, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("发布清单格式错误：%w", err)
	}
	if m.Product != Product {
		return nil, fmt.Errorf("发布清单的产品不是 %s：%q", Product, m.Product)
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return nil, err
	}
	if m.MinUpgradableFrom != "" {
		if _, err := ParseVersion(m.MinUpgradableFrom); err != nil {
			return nil, err
		}
	}
	for _, a := range append(append([]Artifact(nil), m.Artifacts...), m.Installer) {
		if !sha256Pattern.MatchString(a.SHA256) || a.Size <= 0 || a.File == "" {
			return nil, fmt.Errorf("发布清单中的文件 %q 信息不完整", a.File)
		}
	}
	return &m, nil
}

// ArtifactFor 返回指定系统与架构的二进制。
func (m *Manifest) ArtifactFor(os, arch string) (*Artifact, bool) {
	for i := range m.Artifacts {
		if m.Artifacts[i].OS == os && m.Artifacts[i].Arch == arch {
			return &m.Artifacts[i], true
		}
	}
	return nil, false
}

// CheckUpgrade 检查能否从当前版本升级到清单版本（设计 29.7.2、29.7.4）：
// 清单版本必须高于当前版本（除非在本机显式允许降级），当前版本不低于 min_upgradable_from。
func (m *Manifest) CheckUpgrade(current string, allowDowngrade bool) error {
	target, err := ParseVersion(m.Version)
	if err != nil {
		return err
	}
	cur, err := ParseVersion(current)
	if err != nil {
		return fmt.Errorf("无法识别当前版本 %q，不能判断是否降级", current)
	}
	switch c := target.Compare(cur); {
	case c == 0:
		return fmt.Errorf("已是 %s，无需升级", m.Version)
	case c < 0 && !allowDowngrade:
		return fmt.Errorf("拒绝降级：当前 %s，目标 %s（确需降级时在本机加 --allow-downgrade）", current, m.Version)
	}
	if m.MinUpgradableFrom != "" {
		min, _ := ParseVersion(m.MinUpgradableFrom)
		if cur.Compare(min) < 0 {
			return fmt.Errorf("当前 %s 低于该版本要求的最低版本 %s，请先升级到中间版本", current, m.MinUpgradableFrom)
		}
	}
	return nil
}
