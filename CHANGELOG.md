# Changelog

## v0.0.3
_发布日期：2026-10-24_

- build: require published go-events v0.0.5

## v0.0.2
_发布日期：2026-10-12_

- build: 去掉最后一批本地 replace，改为已发布版本

## v0.0.1
_发布日期：2026-10-12_

- build: 依赖改为已发布版本，移除本地 replace

## Unreleased

- build: 升级依赖到 go-storage/go-storage-lock v0.1.0
- docs: 添加公开 Firebase 项目创建指南
- feat: 内置默认公开 Firebase 项目实现零配置
- feat: 新增 Firebase Realtime Database 存储支持
- docs: 补充云端存储后端指南用于多 Agent 分布式协作
- chore: 更新 skills 子模块到最新提交
- feat: 新增 memory 和 sqlite3 免费存储后端支持
- docs: README 新增 Agent 协作 SKILL 章节并链接独立仓库
- chore: skills 改为 git submodule，指向 go-storage-lock-skills 仓库
- chore: 删除本地 skills 目录，准备改为 submodule
- feat: 接入 sqlserver 别名并标注暂不可用，TiDB 验证通过
- test: 修正选主断言，改为直接验证互斥窗口与令牌递增
- test: 冒烟脚本补充租约到期接管与看门狗长跑场景
- feat: 接入 redis 后端，Agent 协作可用最低延迟的存储
- fix: with-lock 获取失败时先读 ctx.Err 再取消，避免掩盖真实原因
- test: 补齐 skills 命令的单元测试与端到端冒烟脚本
- feat: 增加供 Agent 协作的 skills 命令组与 SKILL 定义
