# D10 · 部署、运维与开源工程化

> [English version](../../design/10-deployment-and-ops.md) · [ai-gateway 文档套件](../README.md)的一部分

| | |
| --- | --- |
| **阶段** | P0（compose、PostgreSQL、测试、CI、文档） · P3（Helm、SQLite 演示） |
| **依赖** | [D05 可观测性](05-observability.md)（探针/看板随部署产物交付） |
| **被依赖** | 其余全部设计——本文是让它们可被采用的信誉层 |

## 背景

现状：一个 Dockerfile、一个 Makefile、`configs/config.yaml`，以及仓库根目录里一个已编译的 `server.exe`。没有 compose 文件、没有 CI、没有测试（全树零个 `*_test.go`）、仅支持 MySQL、文档仅中文，`go.mod` 还有内务问题（CLAUDE.md 记录的模块名漂移；二进制被提交进 git）。对开源基础设施项目而言，"首个请求耗时"与可见的工程卫生*就是*漏斗顶端——本文列 P0 不是因为它光鲜，而是因为没有它，其他一切都发布不出去。

## 部署形态矩阵

| 层级 | 栈 | 状态 | 目标用户 |
| --- | --- | --- | --- |
| **演示**（P3） | 单二进制 + SQLite + 内存配额兜底 | 一个文件 | "60 秒试一把"——无 Redis：配额/熔断降级为单实例内存版并在启动时告警 |
| **标准**（P0） | `docker compose up`：网关 + MySQL（或 PG）+ Redis（+ 可选 Prometheus/Grafana profile） | 卷 | 评估 → 小规模生产 |
| **生产 HA**（P0 文档、P3 Helm） | LB 后 N 个网关副本；托管 DB 与 Redis；ES 可选 | 外部 | 平台团队 |

### 高可用声明（说清楚，并保证它是真的）

网关按设计无状态：所有协调状态在 Redis（Lua 配额窗口、并发 ZSET、熔断状态、缓存），所有持久状态在 DB，跨实例缓存失效经现有 `ai:gw:key:invalidate` pub/sub。因此：任何副本服务任何请求；滚动升级 = `readyz` 翻转排水（[D05](05-observability.md)）；故障域是 Redis（配额/熔断/计费闸门降级——按设计原则 6 失败开放，指标高声报警）与 DB（管理面不可用；代理路径靠缓存撑到 TTL）。每个新功能的设计必须说明其状态落在这条线的哪一侧——这是评审清单项。

### Kubernetes（P3）

Helm chart 放 `deploy/helm/`：Deployment + HPA（CPU + 可选基于 `aigw_concurrency_slots` 的自定义指标）、PDB、探针接 `/healthz`/`readyz`、加密密钥与 DB/Redis DSN 走 secret、可选 ServiceMonitor。**Operator 明确推迟**到出现 CRD 形状的需求（如舰队规模的声明式租户/提供方供给）——chart 已覆盖 P3 出口标准（3 副本 HA 通过故障转移测试）。

## 多数据库支持（P0：PostgreSQL · P3：SQLite）

### 决策（ADR）

- **背景：** 仅 MySQL 砍半受众；GORM 已抽象了大部分方言面。
- **决策：** P0 起官方支持 MySQL 8 + PostgreSQL 15+，SQLite 仅用于演示层（永不写进生产文档）。配置键 `database.driver` 选择 GORM 驱动。
- **兼容性审计清单**（这个代码库里真正有差异的点）：
  - `gorm.io/datatypes.JSON` —— 各驱动映射为 `json`/`jsonb`/`text`；JSON 路径*查询*必须走 `datatypes.JSONQuery` 而非手写 `->>` SQL（排查所有手写 JSON 谓词）。
  - `AUTO_INCREMENT` vs 序列、`datetime` 精度、varchar uniqueIndex 的索引长度限制（`key_hash`）、PG 的 `LIKE` 大小写敏感——通过只在 GORM tag 中声明 schema、不写裸 DDL 来覆盖。
  - 裸 SQL 排查：所有 `db.Raw`/`db.Exec` 字符串需要方言评审（`ListAuditSessions` 里的审计会话聚合查询是最可能的点）。
- **迁移：** 只要路线图不变量 2（只做加法）成立，`autoMigrate` 继续作为机制。一旦破坏性变更不可避免，即采用版本化迁移（golang-migrate）——现在就把触发条件记下来，让它成为计划而不是争论。
- **后果：** CI 对两个引擎跑全量套件（矩阵任务）；SQLite 跑冒烟子集。

## 测试策略（P0）

金字塔，对零测试基线保持务实：

1. **单元（`internal/biz`）** —— P0 覆盖率 ≥ 60% 门槛的对象。优先级 = 风险序：`quota.go` 的 Lua 行为（miniredis）、`router.go` 策略/熔断（[D01](01-routing-and-lb.md)）、`credits.go` 计价数学、`key_cache.go` 失效、护栏链语义。Kratos 分层的 repo 接口让 DB fake 便宜；Redis 行为测试在 miniredis 的 Lua 支持足够时用它，不够时落到 docker 化的集成层。
2. **集成（`test/integration`）** —— docker-compose 支撑（testcontainers-go）：真 MySQL/PG/Redis，httptest 伪提供方。承载每篇设计文档列出的流程测试：故障转移、双租户泄漏、计费冻结/结算不变量、缓存跨方言、快照升级迁移。
3. **E2E** —— Playwright 控制台流程（[D08](08-web-console.md)）+ 脚本化的 OpenAI SDK / Anthropic SDK 客户端对 compose 栈运行。

## CI/CD（GitHub Actions，P0）

| 工作流 | 触发 | 任务 |
| --- | --- | --- |
| `ci.yml` | PR + main | `go vet` + `golangci-lint` → 单测（race）+ 覆盖率门槛 → 集成矩阵 {mysql, postgres} → `wire ./cmd/server` && `git diff --exit-code`（生成代码新鲜度）→ 控制台构建 + Playwright（按路径过滤） |
| `release.yml` | tag `v*` | goreleaser：多架构二进制（linux/amd64+arm64、darwin、windows）+ docker buildx 多架构推送 + SBOM + 校验和 + changelog 草稿 |
| `nightly.yml` | cron | 支付沙箱测试（凭证门控）、依赖审计（`govulncheck`）、docker 基础镜像刷新 |

版本：SemVer；P1 完成前保持 `v0.x`，`v1.0` = P1 出口标准全部满足（届时 API 稳定承诺同时覆盖 `/ai/v1/*` *与*管理 API 信封）。

## 仓库卫生（P0，一次性）

- 把 `go.mod` 模块名修正为 `github.com/opscenter/ai-gateway`（CLAUDE.md 常备注记）；从 git 移除 `server.exe` 并 `.gitignore` 构建产物。
- 根文档：`README.md` 用英文重写（徽章、compose 60 秒快速开始、功能矩阵、指向 `docs/` 的链接），加 `README.zh-CN.md`、`CONTRIBUTING.md`（构建、wire、测试、PR 约定——大体可从 CLAUDE.md 提炼）、`SECURITY.md`（私密披露渠道、受支持版本）、`CODE_OF_CONDUCT.md`、issue/PR 模板、`CHANGELOG.md`（keep-a-changelog）。
- 许可证：MIT 已就位——确认文件头方针（无需）与控制台构建的第三方声明。
- **OpenAPI 规范**（`api/openapi.yaml`）覆盖管理 API：初期手工维护（此处的 Kratos HTTP 是手写路由，不是 proto 生成），由 CI 契约测试校验，并作为控制台生成客户端的来源（[D08](08-web-console.md)）——这正是让它保持诚实的机制。

## 配置与运维打磨（P0）

- 所有配置键可经环境变量覆盖（`AIGW_` 前缀映射）——compose/k8s 人体工学；机密（加密密钥、DSN、admin token）以环境变量优先方式写文档。
- 启动时配置校验并给出可执行的错误（32 字节密钥检查已有；扩展到 DSN/地址合理性、非开发模式下 admin token 为空的告警）。
- 优雅停机排水顺序文档化并测试：readyz→503、HTTP 排水、worker 队列冲刷（审计/计费）、关闭。
- `server doctor` 子命令：检查 DB/Redis 连通性、Redis 版本特性（[D07](07-caching-strategies.md) 的向量支持）、加密密钥有效性、待迁移项——支持工作让用户跑的第一条命令。

## 涉及代码

| 位置 | 变更 |
| --- | --- |
| `deploy/compose/docker-compose.yml`（+ grafana/prometheus profile）、`deploy/helm/`（P3） | 新增 |
| `.github/workflows/`、`.golangci.yml`、`.goreleaser.yml` | 新增 |
| `internal/data/data.go` | 驱动选择；方言审计修正 |
| `cmd/server/main.go` | `doctor`、`rekey` 子命令；环境变量覆盖 |
| `go.mod`、`.gitignore`、根文档、`api/openapi.yaml` | 上述卫生项 |
| `test/integration/`（新增） | testcontainers 套件 |

## 验证

本文的交付物*就是* P0 出口标准（[路线图](../03-roadmap.md)）：干净机器上仅按 README 操作，compose 到首个请求不超过 10 分钟；两个数据库上 CI 全绿且过覆盖率门槛；打 tag 即产出可安装的多架构产物。元验证：在一台从没见过这个仓库的机器上拿秒表跑一遍快速开始。

## 实现说明（ADR 补记）

**模块路径已从 `github.com/opscenter/ai-gateway` 迁移到 `github.com/adcwb/ai-gateway`。** 上面的仓库卫生条目早期修正过一次模块名；如今为跟上实际的 GitHub 远程仓库（`github.com/adcwb/ai-gateway`，项目托管与发布均在此）又迁移了一次。所有内部 import、`guardrail.proto` 的 `go_package` 选项、`webhook-logger` 示例自身的模块声明，以及 Helm chart 默认的 `image.repository`（`ghcr.io/adcwb/ai-gateway`，与 `release.yml` 的 `ghcr.io/${{ github.repository }}` 保持一致）均在同一次改动中一并更新——模块改名必须全量落地，因为 Go 是按声明的 import 路径解析内部包的。
