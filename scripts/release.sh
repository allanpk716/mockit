#!/usr/bin/env bash
# mockit 发版组装脚本:交叉构建四平台二进制 + 收集三文档,归入 dist/release-v<版本>/,
# 最后打印(不执行)gh release create 命令。
#
# 纪律:本脚本不推送、不创建 release、不 force、不碰远端;实际发版是人工动作。
# 产物只落 dist/(已 gitignore,不入库)。
#
# 用法:
#   ./scripts/release.sh                 # 目标 tag 默认 v0.1.1
#   TAG=v0.1.2 ./scripts/release.sh      # 覆盖目标 tag(内嵌版本须与之对齐,否则出厂检查拒绝)
#
set -euo pipefail

fail() { echo "release: 错误: $*" >&2; exit 1; }

# 脚本定位仓根,允许从任意目录调用
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

TAG="${TAG:-v0.1.1}"
# tag 格式守门(防误传导致后续路径操作出格)
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "TAG 需形如 v0.1.1,当前为 \"${TAG}\""
EXPECTED_VERSION="${TAG#v}"   # tag v0.1.1 ↔ 内嵌版本 0.1.1

HOST_EXE=""
if [ "$(go env GOOS)" = "windows" ]; then HOST_EXE=".exe"; fi

RELEASE_DIR="dist/release-${TAG}"
BINARIES=(
  mockit-windows-amd64.exe
  mockit-darwin-amd64
  mockit-darwin-arm64
  mockit-linux-amd64
)
DOCS=(
  INSTALL.md
  AGENT-GUIDE.md
  skills/mockit/SKILL.md
)

echo "==> mockit 发版组装:目标 tag ${TAG}(内嵌版本须为 ${EXPECTED_VERSION})"

# ---------- ① 出厂检查:本地构建临时产物,运行 version,输出必须等于目标 tag 版本 ----------
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
FACTORY_BIN="$TMP_DIR/mockit-factory-check${HOST_EXE}"

echo "==> 出厂检查:构建本机临时产物,核对 version 输出 = ${EXPECTED_VERSION}"
go build -o "$FACTORY_BIN" .
BUILT_VERSION="$("$FACTORY_BIN" version)"
if [ "$BUILT_VERSION" != "$EXPECTED_VERSION" ]; then
  fail "内嵌版本(${BUILT_VERSION})与目标 tag(${TAG} → ${EXPECTED_VERSION})不一致;请先修改 main.go 的 Version 常量为 \"${EXPECTED_VERSION}\" 再发版。"
fi
echo "    version 输出 ${BUILT_VERSION},与 ${TAG} 对齐。"

# ---------- 前置:三文档齐备检查(缺失即明确报错,不进入耗时的交叉构建) ----------
for doc in "${DOCS[@]}"; do
  [ -f "$doc" ] || fail "缺文档:${doc} 不存在。发版四件套要求三文档齐备(仓根 INSTALL.md、AGENT-GUIDE.md、skills/mockit/SKILL.md),请先就位再组装。"
done

# ---------- ② 四平台交叉构建(产物名沿用 dist 现名) ----------
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR"
echo "==> 交叉构建四平台产物 → ${RELEASE_DIR}/"
PLATFORMS=(
  "windows amd64"
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
)
for i in "${!PLATFORMS[@]}"; do
  read -r goos goarch <<<"${PLATFORMS[$i]}"
  name="${BINARIES[$i]}"
  echo "    构建 ${goos}/${goarch} → ${name}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -o "${RELEASE_DIR}/${name}" .
done

# ---------- ③ 收集三文档(平铺归入 release 目录,与 gh release 资产面一致) ----------
echo "==> 收集文档 → ${RELEASE_DIR}/"
for doc in "${DOCS[@]}"; do
  cp "$doc" "$RELEASE_DIR/"
  echo "    ${doc} → $(basename "$doc")"
done

# ---------- SHA256SUMS.txt:四平台二进制的 sha256(供安装侧核对) ----------
echo "==> 生成 SHA256SUMS.txt(四平台二进制)"
(
  cd "$RELEASE_DIR"
  sha256sum "${BINARIES[@]}" > SHA256SUMS.txt
)

# ---------- ④ 打印发版命令(不执行;发版是人工动作) ----------
echo
echo "==> 组装完成:${RELEASE_DIR}/ 内容:"
ls -l "$RELEASE_DIR"
echo
echo "==> 发版命令(人工执行;本脚本不会推送或创建 release):"
echo
echo "    gh release create ${TAG} \\"
for name in "${BINARIES[@]}"; do
  echo "        ${RELEASE_DIR}/${name} \\"
done
echo "        ${RELEASE_DIR}/INSTALL.md \\"
echo "        ${RELEASE_DIR}/AGENT-GUIDE.md \\"
echo "        ${RELEASE_DIR}/SKILL.md \\"
echo "        ${RELEASE_DIR}/SHA256SUMS.txt \\"
echo "        --title \"mockit ${TAG}\" \\"
echo "        --notes \"mock 页面审核闭环服务 ${TAG}:四平台二进制 + INSTALL.md(安装引导)+ AGENT-GUIDE.md(agent 使用指南)+ SKILL.md(技能文件),SHA256SUMS.txt 供下载后核对,安装请让目标机器 agent 按最新 release 的 INSTALL.md 执行。\""
echo
echo "提醒:实际创建 release 是人工动作(需网络与 gh 登录),请人工核对上述命令后再执行。"
