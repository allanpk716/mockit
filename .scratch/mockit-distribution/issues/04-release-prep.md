# 票 04 · 发版准备:版本常量 bump + 发布组装脚本(不自动发布)

## What to build

两件事:(1)`main.go` 版本常量 0.1.0→0.1.1(发布元数据,其余代码零改动);(2)新增 `scripts/release.sh`:组装 v0.1.1 四件套并打印发版命令,**不执行发布**。

## 验收标准

- [ ] `main.go` 仅 `Version` 常量一行变化("0.1.0"→"0.1.1"),无其他改动
- [ ] `go vet ./...` 通过;`go build` 成功;构建产物运行 `version` 子命令输出 `0.1.1`
- [ ] `scripts/release.sh` 存在且 `bash -n` 语法通过
- [ ] 脚本行为:①出厂检查——先本地构建临时产物并运行 `version`,输出与目标 tag(默认 v0.1.1,可 `TAG=v0.1.2 ./scripts/release.sh` 覆盖)比对,不一致退出非零并提示先改 main.go;②四平台交叉构建(GOOS/GOARCH:windows/amd64、darwin/amd64、darwin/arm64、linux/amd64,产物名沿用 dist 现名 `mockit-<os>-<arch>[.exe]`);③收集三文档(仓根 INSTALL.md、AGENT-GUIDE.md、skills/mockit/SKILL.md)连同二进制归入 `dist/release-v<版本>/`;④**打印**(不执行)完整 `gh release create` 命令(tag + 四件套资产列表 + 附注一句),提示发版是人工动作
- [ ] 脚本不推送、不创建 release、不 force、不碰远端;产物只落 dist/(已 gitignore,不入库)
- [ ] 产物目录有 SHA256SUMS.txt(四平台二进制的 sha256,供安装侧核对)

## Blocked by

无,可立即开始。

## 涉及路径

- main.go
- scripts/release.sh(新增)

## 副作用声明

- 运行 `go vet ./...` 与 `go build`(含交叉构建)——产物落 `dist/`(gitignored)
- `bash -n scripts/release.sh` 语法检查
- 可完整干跑一次 `./scripts/release.sh`(只组装不发版;网络零依赖,gh 仅出现在打印的命令文本里)

decision_refs: D12, D14, D15
review_blocks: 无
