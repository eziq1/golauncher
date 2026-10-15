# 存储与 schema 迁移

对应问题：R10（见 [requirements.md](requirements.md)）。

> 配置和数据库的 schema 支持 migrate 吗？如果以后想升级数据库 schema 怎么办？

## 1. 存储一览

| 存储 | 位置 | 内容 | 版本记录方式 |
|------|------|------|--------------|
| 配置 | `%APPDATA%\golauncher\config.json`（可以随漫游配置同步） | 快捷键、主题、插件开关和偏好等 | 文件里的 `schemaVersion` 字段 |
| 主数据库 | `%LOCALAPPDATA%\golauncher\data\golauncher.db` | 插件 KV、使用频率（frecency）、最近使用 | SQLite `PRAGMA user_version` |
| 剪贴板历史 | `%LOCALAPPDATA%\golauncher\data\plugins\builtin.clipboard\clipboard.db` 和 `images\` | 剪贴板记录、FTS5 全文索引、图片 | SQLite `PRAGMA user_version` |
| 密码类偏好 | Windows 凭据管理器，目标名 `golauncher/<插件ID>/<偏好名>` | 密钥 | 不需要版本 |

数据库放在本地目录（`%LOCALAPPDATA%`），原因有两个：剪贴板历史不能随漫游配置同步到服务器；SQLite 的 WAL 模式也不支持网络共享目录。0.1.0 之前的版本把数据库放在 `%APPDATA%`，启动时会自动迁移到新位置（`internal/app/migrate.go`）。

## 2. 迁移机制

### 2.1 SQLite（主数据库、剪贴板数据库）

```go
// internal/store/store.go
var migrations = []string{
	0: ``,
	1: `CREATE TABLE IF NOT EXISTS kv (...); CREATE TABLE IF NOT EXISTS usage (...);`,
	// 2: `ALTER TABLE usage ADD COLUMN ...;`   ← 以后在末尾追加
}
```

- 打开数据库时读取 `PRAGMA user_version`，从当前版本开始逐级执行 `migrations[v+1..]`。每一级放在一个事务里，并在同一个事务里更新 `user_version`。执行到一半失败时整级回滚，下次启动会重试。
- **只能在末尾追加新的迁移，已经发布的迁移不能修改。** 迁移语句要能重复执行，比如用 `IF NOT EXISTS`。
- 在加版本号之前创建的数据库，`user_version` 为 0，但表已经存在；迁移 1 本身就能重复执行，所以可以直接升级上来。主数据库的这套机制是 v0.1.0 发布前补上的，基线版本为 1。
- **遇到比程序更新的版本**（比如降级安装后）：拒绝打开，返回 `store.ErrNewerSchema`，不会去改写更新版本的数据。

### 2.2 配置文件

- `config.Load` 读取 `schemaVersion`，经过 `migrate()` 升级到当前版本。以后在 `migrate()` 里按版本追加转换步骤即可。
- 遇到比程序更新的版本，或者文件损坏（比如手动改坏了）时，把原文件改名为 `config.json.bad` 保留下来，改用默认配置，并弹通知告诉用户（`config.Store.Recovered`）。
- 写入时先写临时文件，`fsync` 后再改名替换，断电也不会留下一个空的配置文件。
- 导入配置（设置 › 高级）走同一套校验和迁移流程，未知字段一律拒绝。

## 3. 以后升级 schema 的步骤

1. 在对应包的 `migrations` 末尾追加一级，写成可以重复执行的语句。配置文件则把 `SchemaVersion` 加 1，并在 `migrate()` 里补上转换逻辑。
2. 补测试：从上一个版本的数据库或配置出发，迁移后数据完整。参考 `TestMigrations`、`TestOpenRecoversFromBadFile`。
3. 在 CHANGELOG 里注明 schema 有变化。降级到旧版本时，旧版本会拒绝打开新数据库，并提示用户。

## 4. 待改进

- 主数据库遇到更新版本时，目前程序直接无法启动（错误写进日志）。应该像配置文件一样给出明确提示，引导用户升级程序。
