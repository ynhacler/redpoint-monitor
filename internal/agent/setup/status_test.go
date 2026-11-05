package setup

import (
	"os"
	"testing"
)

// PanelMirror 用本机配置的面板地址；没有配置时为空，本机升级就不回退（设计 27.5.3）。
func TestPanelMirror(t *testing.T) {
	p := Paths{ConfDir: t.TempDir()}
	if got := p.PanelMirror(); got != "" {
		t.Errorf("没有配置时应为空：%q", got)
	}
	if err := os.WriteFile(p.envFile(), []byte("VPSMON_SERVER=https://panel.example/\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got := p.PanelMirror(); got != "https://panel.example/releases" {
		t.Errorf("PanelMirror = %q", got)
	}
}
