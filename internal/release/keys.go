package release

// 官方发布公钥（设计 29.7.1、29.7.3）。编译进 Agent 与面板，用于校验发布清单与安装脚本的签名。
//
// 两把：current 用于日常签发；next 平时离线保存，current 泄露时用 next 签发紧急版本。
// 【安全】这里只有公钥。对应的私钥由开发者用 minisign 在离线机器上生成与保管（minisign -G），
// 永远不进入仓库、CI 或任何面板。更换公钥需要发布一个由旧 current 签名的版本。
//
// 公钥为 minisign 公钥文件的第二行（base64）。私钥由开发者离线保管：current 在开发者签名机，next 离线备份。
var officialKeys = []string{
	"RWRvxJiJUet21SDEV8XFOSShkV7Wbn/ZsQhL/dpS5VVB781sfhsqwMu9", // current（2026-10-03 生成）
	"RWR+mBTiANUomgVn75uz3om58EKm81G/0yIheQ9aQTKnjI+bp9oorf6H", // next（备用，只在 current 泄露时使用）
}

// TrustedKeys 返回编译进程序的官方公钥。
func TrustedKeys() []PublicKey {
	out := make([]PublicKey, 0, len(officialKeys))
	for _, s := range officialKeys {
		if k, err := ParsePublicKey(s); err == nil {
			out = append(out, k)
		}
	}
	return out
}
