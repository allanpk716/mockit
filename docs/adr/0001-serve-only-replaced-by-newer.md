# serve 只被更新的 MCP 替换,serve API 对旧 MCP 向后兼容

升级只换磁盘上的二进制,已运行的 Claude Code 里的 MCP 进程仍是旧代码,直到重启 Claude Code。若沿用"版本不等即停旧起新",旧 MCP 会停掉新 serve、再拉起的又是新二进制,多会话间反复互踢。故 MCP 只替换版本**低于**自己的 serve;遇到更新的 serve 就直接使用,并提示用户重启 Claude Code。

## Consequences

- serve 的 HTTP API 必须对旧版 MCP 向后兼容:只加不改不删;确需破坏性变更时须另行设计迁移,不能靠"版本不等就重启"兜底。
- 不含此规则的已装版本(≤0.2.0)升到首个含此规则的版本时,仍须按 INSTALL 手动升级一次。
