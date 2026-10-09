# mockit · 安装引导(INSTALL)

> 入口话术:在目标机器上,用户对本机 agent 说一句——**"按 mockit 最新 release 的 INSTALL.md 装"**。agent 照本文档逐步执行;人肉照做同样成立。装完后该机所有项目零逐项目配置(用户级 MCP 配置 + 全局技能,一次安装全机生效)。

release 页:<https://github.com/allanpk716/mockit/releases>(四平台二进制 + INSTALL.md + AGENT-GUIDE.md + SKILL.md,同 tag 对齐,自 v0.1.1 起)。

## 适用范围与前置条件

- 适用于**自家受信网络内的机器**(见下方安全边界),操作系统为 Windows / macOS / Linux(x64),接入方为支持 MCP 的 agent(Claude Code 类)。
- 本文档自包含:只依赖目标机能访问 GitHub release,不需要本仓库(`git clone` 都不需要),也不会创建任何 release。
- 前置:本机已安装 Claude Code(存在用户级配置 `~/.claude.json`;若尚未运行过 Claude Code,步骤 4 会创建最小骨架);`gh` 与 curl 二选一(都没有时 Windows 用 PowerShell、macOS/Linux 装任一均可,见各步的两种取法)。

**安全边界(先读再用)**:服务默认监听 `0.0.0.0` 且无鉴权——安全边界是网络拓扑,只在受信网络(如 NetBird)运行,勿暴露公网。

## 安装总览

1. 判平台,下载二进制 → 2. 重命名为统一命令名并落位 → 3. 加入 PATH → 4. 按合并纪律写用户级 MCP 配置 → 5. 装全局技能 → 6. 两级验收。

Windows 的命令用 PowerShell,macOS/Linux 用 bash。安装目录约定:Windows 为 `%LOCALAPPDATA%\mockit`(即 `C:\Users\<用户>\AppData\Local\mockit`);macOS/Linux 为 `~/bin`(推荐,免 sudo)或 `/usr/local/bin`(已在默认 PATH,免做步骤 3)。

## 步骤 1:判平台,下载二进制

平台与 release 资产名、落位名的对应关系:

| 平台 | release 资产名 | 落位名(步骤 2) |
|---|---|---|
| Windows(x64) | `mockit-windows-amd64.exe` | `mockit.exe` |
| macOS Intel | `mockit-darwin-amd64` | `mockit` |
| macOS Apple Silicon | `mockit-darwin-arm64` | `mockit` |
| Linux(x64) | `mockit-linux-amd64` | `mockit` |

macOS 先判芯片:`uname -m` 输出 `arm64` = Apple Silicon(用 darwin-arm64),`x86_64` = Intel(用 darwin-amd64)。Windows 与 Linux 各只有一个资产,无需判断。

**Windows(PowerShell,下载到当前目录):**

```powershell
# 取法一:gh(需已安装并登录 gh)
gh release download --repo allanpk716/mockit --pattern 'mockit-windows-amd64.exe' --dir .
# 取法二:无 gh,直链固定 URL(始终指向最新 release)
Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/allanpk716/mockit/releases/latest/download/mockit-windows-amd64.exe' -OutFile .\mockit-windows-amd64.exe
```

**macOS(bash):**

```bash
uname -m   # arm64 → 下面的资产名用 mockit-darwin-arm64;x86_64 → 用 mockit-darwin-amd64
gh release download --repo allanpk716/mockit --pattern 'mockit-darwin-arm64' --dir .   # 取法一
curl -fL -o mockit-darwin-arm64 https://github.com/allanpk716/mockit/releases/latest/download/mockit-darwin-arm64   # 取法二
```

**Linux(bash):**

```bash
gh release download --repo allanpk716/mockit --pattern 'mockit-linux-amd64' --dir .   # 取法一
curl -fL -o mockit-linux-amd64 https://github.com/allanpk716/mockit/releases/latest/download/mockit-linux-amd64   # 取法二
```

说明:要装指定版本而非最新,把直链中的 `releases/latest/download/` 换成 `releases/download/<tag>`(如 `releases/download/v0.1.1/`);`gh` 则加位置参数 `<tag>`。可选:release 附带 `SHA256SUMS.txt`(四平台二进制的 sha256),下载后可核对。

## 步骤 2:重命名为统一命令名,落位(强制)

重命名是强制步骤:MCP 配置里四平台统一写裸命令名 `mockit`(见步骤 4),全靠本步把带平台后缀的资产名规范化。

**Windows(PowerShell):**

```powershell
$dir = "$env:LOCALAPPDATA\mockit"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Move-Item .\mockit-windows-amd64.exe "$dir\mockit.exe" -Force
```

**macOS / Linux(bash,二选一落位):**

```bash
# 方案 A(推荐):~/bin,免 sudo;步骤 3 需把它写进 shell 配置
mkdir -p "$HOME/bin"
mv mockit-darwin-arm64 "$HOME/bin/mockit"   # 换成本机实际的资产名
chmod +x "$HOME/bin/mockit"                 # Unix 装完必须补可执行位
# 方案 B:/usr/local/bin,需 sudo,已在默认 PATH,可跳过步骤 3
# sudo mv mockit-linux-amd64 /usr/local/bin/mockit && sudo chmod +x /usr/local/bin/mockit
```

## 步骤 3:加入 PATH

**Windows(PowerShell,用户级持久值读-改-写):**

```powershell
$dir = "$env:LOCALAPPDATA\mockit"
$old = [Environment]::GetEnvironmentVariable('Path', 'User')          # 只读用户级持久值
if ([string]::IsNullOrEmpty($old)) {
    [Environment]::SetEnvironmentVariable('Path', $dir, 'User')
} elseif (-not ($old -split ';' | Where-Object { $_.Trim() -ieq $dir })) {
    [Environment]::SetEnvironmentVariable('Path', "$($old.TrimEnd(';'));$dir", 'User')   # 保留既有条目,只追加
}
```

两条禁令:

- **禁止 `setx`**:它把 Path 截断到 1024 字符,会毁掉既有条目。
- **禁止读写进程合并值**(`$env:Path`):那是"用户级 + 系统级 + 会话临时"拼出来的快照,写回会把系统级条目复制进用户级,造成污染与重复。上文只碰用户级持久值。

写入对新进程生效;已开的终端不会自动刷新。

**macOS / Linux(bash,仅方案 A ~/bin 需要;方案 B 跳过):**

```bash
case "$(basename "$SHELL")" in
  zsh) RC="$HOME/.zshrc" ;;   # macOS 默认
  *)   RC="$HOME/.bashrc" ;;
esac
touch "$RC"
grep -qF 'export PATH="$HOME/bin:$PATH"' "$RC" || echo 'export PATH="$HOME/bin:$PATH"' >> "$RC"
export PATH="$HOME/bin:$PATH"   # 当前终端立即生效;其余终端重开或 source 后生效
```

(fish 等其他 shell 请按各自语法把 `~/bin` 加入 PATH。)

## 步骤 4:写入用户级 MCP 配置(~/.claude.json)

目标文件 `~/.claude.json` 是 Claude Code 的**用户级**配置,机器上往往已有其他 MCP server 与状态键。**强制合并纪律,五步缺一不可:①写前备份 → ②结构化读取 → ③仅在 `mcpServers` 下合并/更新 `mockit` 一个键 → ④JSON 校验 → ⑤写回。明文禁止用模板整文件替换**——那会抹掉目标机既有配置。合并后 `mockit` 键的值四平台统一:

```json
{"command": "mockit", "args": ["mcp"]}
```

**Windows(PowerShell 示例):**直接把整块粘贴进 PowerShell 控制台执行;若要存成 `.ps1` 文件再执行,须以 UTF-8 with BOM 保存——Windows PowerShell 5.1 读无 BOM 脚本时会把中文注释按 ANSI 误解析,直接报语法错误。

```powershell
$cfgPath = "$HOME\.claude.json"
if (-not (Test-Path $cfgPath)) {
    [IO.File]::WriteAllText($cfgPath, '{"mcpServers":{}}')             # 不存在则建最小骨架
}
Copy-Item $cfgPath "$cfgPath.bak-$(Get-Date -Format yyyyMMddHHmmss)"   # ① 写前备份
$cfg = [IO.File]::ReadAllText($cfgPath) | ConvertFrom-Json             # ② 结构化读取
if (-not $cfg.PSObject.Properties['mcpServers']) {                     # ③ 只动 mcpServers 下的 mockit 键
    $cfg | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{})
}
$entry = [pscustomobject]@{ command = 'mockit'; args = @('mcp') }
if ($cfg.mcpServers.PSObject.Properties['mockit']) { $cfg.mcpServers.mockit = $entry }
else { $cfg.mcpServers | Add-Member -NotePropertyName mockit -NotePropertyValue $entry }
$json = $cfg | ConvertTo-Json -Depth 100                               # ④ 校验:能完整序列化-再解析
$json | ConvertFrom-Json | Out-Null
[IO.File]::WriteAllText($cfgPath, $json)                               # ⑤ 写回(UTF-8 无 BOM)
```

**macOS / Linux(python3 示例,只用标准库 json;Windows 有 Python 时把 `python3` 换成 `python`):**

```bash
python3 - <<'PY'
import json, os, shutil, time
p = os.path.expanduser("~/.claude.json")
if not os.path.exists(p):
    open(p, "w", encoding="utf-8").write('{"mcpServers":{}}')          # 不存在则建最小骨架
shutil.copy2(p, f"{p}.bak-{time.strftime('%Y%m%d%H%M%S')}")            # ① 写前备份
cfg = json.load(open(p, encoding="utf-8"))                             # ② 结构化读取
cfg.setdefault("mcpServers", {})["mockit"] = {"command": "mockit", "args": ["mcp"]}   # ③ 只合并这一个键
text = json.dumps(cfg, ensure_ascii=False, indent=2)                   # ④ 序列化并校验
json.loads(text)                                                       #    再解析一次,确保合法
open(p, "w", encoding="utf-8").write(text)                             # ⑤ 写回
PY
```

检查(只读,不改文件):

```bash
python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.claude.json')))['mcpServers']['mockit'])"
```

```powershell
([IO.File]::ReadAllText("$HOME\.claude.json") | ConvertFrom-Json).mcpServers.mockit
```

结构化读写会把缩进规范为 2 空格,键的内容不变;这是合并的正常形态,与"整文件模板替换"(内容丢失)是两回事。不需要先起 serve——MCP 首次调用工具时会自动拉起本机 serve。

## 步骤 5:安装全局技能

把 release 附带的技能文件(仓内正本路径 `skills/mockit/SKILL.md`,release 资产名平铺为 `SKILL.md`)放到用户级技能目录,任何项目里的 agent 都会被触发式技能覆盖(用户提出要做 mock 页面、要对比多个候选拍板时,自动按技能工作流提交)。

**Windows(PowerShell):**

```powershell
Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/allanpk716/mockit/releases/latest/download/SKILL.md' -OutFile .\SKILL.md
New-Item -ItemType Directory -Force -Path "$HOME\.claude\skills\mockit" | Out-Null   # 目录不存在则建
Move-Item .\SKILL.md "$HOME\.claude\skills\mockit\SKILL.md" -Force
```

**macOS / Linux(bash):**

```bash
curl -fL -o SKILL.md https://github.com/allanpk716/mockit/releases/latest/download/SKILL.md   # 或:gh release download --repo allanpk716/mockit --pattern 'SKILL.md' --dir .
mkdir -p "$HOME/.claude/skills/mockit"   # 目录不存在则建
mv SKILL.md "$HOME/.claude/skills/mockit/SKILL.md"
```

## 步骤 6:两级验收

**立即验收(不依赖 PATH):用下载文件的绝对路径运行 `version`,输出必须等于 release tag 去掉 `v` 前缀**(tag `v0.1.1` → 输出 `0.1.1`)。不符说明资产下错或版本不齐,回步骤 1。

```powershell
# Windows
& "$env:LOCALAPPDATA\mockit\mockit.exe" version
# 自动比对(可选):取最新 tag 与本机输出比对
$tag = (Invoke-RestMethod 'https://api.github.com/repos/allanpk716/mockit/releases/latest').tag_name
$v = & "$env:LOCALAPPDATA\mockit\mockit.exe" version
if ($v -eq $tag.TrimStart('v')) { "版本对齐:$tag" } else { "版本不符:期望 $($tag.TrimStart('v')),实际 $v" }
```

```bash
# macOS / Linux(按实际落位目录)
"$HOME/bin/mockit" version            # 方案 B 换成 /usr/local/bin/mockit version
# 自动比对(可选)
TAG=$(curl -fsSL https://api.github.com/repos/allanpk716/mockit/releases/latest | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)
V="$("$HOME/bin/mockit" version)"
if [ "$V" = "${TAG#v}" ]; then echo "版本对齐:$TAG"; else echo "版本不符:期望 ${TAG#v},实际 $V"; fi
```

**最终验收:重启 Claude Code 进程**——退出整个 Claude Code 进程再启动,**仅开新会话不够**(旧进程不会重读 PATH、`~/.claude.json` 与技能目录)。本次重启同时使三样东西生效:PATH、MCP 配置、技能。新会话中:

1. 工具列表出现 `mockit_submit` / `mockit_get_review` / `mockit_list` 三个工具,即接入成功。
2. 可选冒烟:提交一条最小提交(如 title=「mockit 安装冒烟」,1 个候选,`html` 内联任意极小页面),拿到 id 与审核页 URL 即通过;不要求用户裁决,放着 14 天自动清理。首次调用会自动拉起 serve,多花几秒属正常。

日常使用:接入方 agent 参考 release 附带的 AGENT-GUIDE.md(工作流、错误应对);技能会在用户要做 mock 页面时自动触发。

## 排障

| 症状 | 原因与处置 |
|---|---|
| 手机打不开审核页 URL | NetBird 前提:URL 主机名来自本机 NetBird 网段(100.64.0.0/10)探测或 `external_url` 配置,**手机与机器同在 NetBird 网络才可达**。 |
| submit 报"无法确定手机可达地址…配 external_url" | 本机探测不到唯一的 NetBird IPv4。在 `~/.mockit/config.json` 写 `"external_url": "裸主机名"`(域名或 IP 字面量,含 IPv6;**不带端口、不带 `http://`**,带了 serve 启动即报配置错误),改回裸主机名后重试 submit;若仍报旧错,结束 serve 进程让下次调用自动重新拉起。 |
| 旧链接 404 | serve 重启发生端口漂移(被占自动 +1),旧 URL 失效;提交数据仍在。重新提交取新 URL,或从根路径(列表页)进入。 |
| MCP 报"启动中退出,可手工运行 mockit serve 查看报错" | 自动拉起的 serve 因配置等问题退出。手工开一个终端运行 `mockit serve` 看具体报错(常见:external_url 带了端口或前缀)。 |
| 看 serve 实际端口 / 运行状态 | 实际端口与 PID 在 `~/.mockit/server.lock`;日志在 `~/.mockit/logs/serve.log`(同时回显 stderr)。 |
| Windows 重启 Claude Code 后仍找不到 `mockit` 命令 | PATH 写的是用户级持久值,只对新进程生效。先开**新终端**跑 `mockit version` 确认;仍不行则检查步骤 3 是否写入成功;兜底可把 `~/.claude.json` 里 `mcpServers.mockit.command` 改为二进制绝对路径(同样可用)。 |
| `gh` 未安装或未登录 | 全部步骤都有 curl / Invoke-WebRequest 直链取法(见各步"取法二");最新 tag 从 <https://github.com/allanpk716/mockit/releases/latest> 重定向页查看。 |

## 重装 / 升级

重复执行步骤 1~6 即可,各步幂等:重命名覆盖旧文件、PATH 判重不重复追加、配置只更新 `mockit` 一个键、技能覆盖同名文件;最后照旧重启 Claude Code 进程。
