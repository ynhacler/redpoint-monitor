#!/bin/sh
# 离线签名并发布一个草稿 Release（设计 29.7）。在保管签名私钥的机器上运行：
#
#   scripts/sign-release.sh v0.2.0 [私钥文件，默认 ~/.minisign/vpsmon.key]
#
# 步骤：下载草稿中的 manifest.json、SHA256SUMS、agent-x.y.z.sh → 核对三者一致 → minisign 签名（需要输入私钥密码）
#       → 上传 .minisig → 确认后把草稿改为公开发布。
# 【安全】私钥只在本机使用；本脚本不会上传、复制或打印私钥。依赖 gh、minisign、shasum / sha256sum。
set -eu

TAG="${1:-}"
KEY="${2:-$HOME/.minisign/vpsmon.key}"
[ -n "$TAG" ] || { echo "用法：$0 vX.Y.Z [私钥文件]" >&2; exit 2; }
case "$TAG" in v*) ;; *) TAG="v$TAG" ;; esac
VERSION="${TAG#v}"
[ -f "$KEY" ] || { echo "✗ 找不到私钥 ${KEY}（用 minisign -G -p vpsmon.pub -s $KEY 生成）" >&2; exit 1; }
command -v minisign >/dev/null 2>&1 || { echo "✗ 需要 minisign（brew install minisign）" >&2; exit 1; }

# gh 在临时目录中无法从 git 推断仓库：先在仓库目录中确定，之后通过 GH_REPO 传给每次调用
GH_REPO="${GH_REPO:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
export GH_REPO
echo "仓库：$GH_REPO"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"
gh release download "$TAG" -p manifest.json -p SHA256SUMS -p "agent-$VERSION.sh"

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }
# 草稿中的三份文件必须互相一致：安装脚本的哈希同时出现在 SHA256SUMS 与 manifest.json 中
SCRIPT_SHA="$(sha "agent-$VERSION.sh")"
grep -q "^$SCRIPT_SHA  agent-$VERSION.sh\$" SHA256SUMS || { echo "✗ SHA256SUMS 与安装脚本不一致，停止" >&2; exit 1; }
grep -q "\"$SCRIPT_SHA\"" manifest.json || { echo "✗ manifest.json 与安装脚本不一致，停止" >&2; exit 1; }
grep -q "\"version\": \"$VERSION\"" manifest.json || { echo "✗ manifest.json 的版本不是 ${VERSION}，停止" >&2; exit 1; }

echo "即将签名 ${TAG}："
sed -n '1,8p' manifest.json
printf '确认清单内容无误后按回车继续（Ctrl+C 取消）'
read -r _

minisign -S -s "$KEY" -m manifest.json -t "vpsmon-agent $VERSION manifest"
minisign -S -s "$KEY" -m SHA256SUMS -t "vpsmon $VERSION SHA256SUMS"
minisign -S -s "$KEY" -m "agent-$VERSION.sh" -t "vpsmon-agent $VERSION installer"
gh release upload "$TAG" manifest.json.minisig SHA256SUMS.minisig "agent-$VERSION.sh.minisig" --clobber
echo "✓ 签名已上传"

printf '公开发布 %s？[y/N] ' "$TAG"
read -r ok
if [ "$ok" = "y" ] || [ "$ok" = "Y" ]; then
  gh release edit "$TAG" --draft=false --notes "vpsmon ${VERSION}。文件均有 minisign 签名，可用官方公钥自行核对：minisign -Vm SHA256SUMS -P <公钥>"
  echo "✓ 已发布 $TAG"
else
  echo "· 仍为草稿；稍后可运行：gh release edit $TAG --draft=false"
fi
