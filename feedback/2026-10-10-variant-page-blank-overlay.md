# 候选页（/s/<id>/v<n>）在手机上整页空白：#stage-msg 的 hidden 被 CSS 覆盖

- 发现时间：2026-10-10
- 发现场景：human-res-cost 项目 #21，提交 `uir64r`（3 个 HTML 候选），用户从审核页点进任一候选，手机上只看到深色空屏 + 底部悬浮条
- 影响范围：所有提交的所有候选页（与候选内容无关）；投票按钮仍可用，但用户看不到要投票的页面

## 现象

- `/s/uir64r/v1`：iframe 区域全黑（`--bg` 深色），底部 pills 与“选它 / 打回”正常显示。
- `/raw/uir64r/1/index.html` 直接打开：页面正常渲染。
- 即 iframe 内容已加载，只是被遮住了。

## 复现与证据

用 Playwright 打开 `http://<host>:8321/s/uir64r/v1`，视口 390×844：

```js
const f = document.getElementById('frame');
// iframe 已加载、尺寸正常、内容存在
{ w: 390, h: 739, bodyBg: "rgb(246, 245, 242)", text: "人力成本看板 …" }

const m = document.getElementById('stage-msg');
{ hidden: true, display: "flex", text: "" }   // ← 应为 display:none
```

## 根因

`internal/server/web/variant.html`：

```css
/* 第 27 行 */
#stage-msg{
  position:absolute;inset:0;display:flex;align-items:center;justify-content:center;
  color:var(--dim);background:var(--bg);padding:24px;text-align:center;font-size:15px;
}
```

```html
<!-- 第 66 行 -->
<div id="stage-msg" hidden></div>
```

`hidden` 属性只靠 UA 样式 `[hidden]{display:none}` 生效，而作者样式 `#stage-msg{display:flex}` 优先级更高，把它覆盖了。结果这个空的、`inset:0`、带不透明深色背景的遮罩永远盖在 iframe 上面。`fail()` 里 `el.hidden = false` 的显示逻辑本身没问题，问题出在“隐藏态”从未真正隐藏。

## 建议修复

二选一：

1. 在 `variant.html` 样式里补一条（最小改动）：

   ```css
   #stage-msg[hidden]{display:none}
   ```

2. 或全局兜底（同时防止其他页面踩同样的坑，如 `#err`、`#note-chip` 若将来加了 display 样式）：

   ```css
   [hidden]{display:none!important}
   ```

建议顺带检查 `internal/server/web/` 下其他页面里“带 `hidden` 属性 + 有 display 样式”的元素。

## 建议补的测试

渲染候选页（有效 id、有效候选）后断言：`#stage-msg` 计算样式 `display === 'none'`，且 iframe 的 `elementFromPoint`（取舞台中心点）命中 `#frame` 而不是 `#stage-msg`。

## 临时绕行（修复前）

先打开 `/raw/<id>/<n>/index.html` 查看候选，再到 `/s/<id>/v<n>` 点底部按钮投票。
