package release

// 官方发布公钥（设计 29.7.1、29.7.3）。编译进 Agent 与面板，用于校验发布清单与安装脚本的签名。
//
// 两把：current 用于日常签发；next 平时离线保存，current 泄露时用 next 签发紧急版本。
// 【安全】这里只有公钥。对应的私钥由开发者用 minisign 在离线机器上生成与保管（minisign -G），
// 永远不进入仓库、CI 或任何面板。更换公钥需要发布一个由旧 current 签名的版本。
//
// TODO(A7)：开发者生成正式密钥后填入（minisign 公钥文件的第二行，base64）。在此之前所有发布校验都会失败，
// 程序会拒绝安装或升级任何版本，这是有意的“失败即拒绝”。
var officialKeys = []string{
	// current:
	// next:
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
