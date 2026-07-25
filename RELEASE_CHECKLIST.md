# AstraMap GitHub Release 发布清单

> 该文档面向项目维护者，用于把“源码可构建”升级为“普通用户可快速安装”。

## 1. 第一阶段：提供预编译二进制

每个正式 Release 至少提供：

```text
amap-linux-amd64.tar.gz
amap-linux-arm64.tar.gz
amap-darwin-amd64.tar.gz
amap-darwin-arm64.tar.gz
amap-windows-amd64.zip
SHA256SUMS
```

建议同时附带：

```text
LICENSE
README.md
QUICKSTART.md
```

## 2. GitHub Actions 自动构建

Release 工作流应完成：

1. 在 Tag 推送时触发，例如 `v0.1.0`
2. 使用固定 Go 版本构建
3. 为 Linux、macOS、Windows 交叉编译
4. 将二进制、许可证和快速部署文档打包
5. 生成 SHA-256 校验文件
6. 上传到 GitHub Release

发布前应在真实系统或 CI Runner 上执行基础验证：

```bash
amap --help
amap index --tree-sitter
```

## 3. README 安装路径

预编译包发布前，README 使用源码构建：

```bash
go build -o amap ./cmd/amap
```

预编译包稳定后，将 README 第一推荐路径改为：

```text
Releases 下载 → 解压 → 放入 PATH → amap install → amap index
```

源码构建保留为第二种安装方式。

## 4. 安装脚本

只有在下载地址、版本解析、校验和验证和失败回滚都实现后，才发布 `install.sh`。

安装脚本至少应具备：

- 自动识别操作系统和 CPU 架构
- 默认下载最新稳定版，也允许指定版本
- 下载后验证 SHA-256
- 默认安装到 `$HOME/.local/bin`
- 不要求 root 权限
- 已有版本覆盖前给出明确提示
- 任何步骤失败时不留下半成品

在脚本经过审计前，不要在 README 中推荐：

```bash
curl ... | sh
```

## 5. 后续分发渠道

按优先级逐步增加：

1. GitHub Release
2. Homebrew Tap
3. Scoop 或 Winget
4. Docker 镜像（Dashboard / HTTP Server 场景）
5. Linux 软件包（deb/rpm，可后置）

## 6. 发布前检查

- [ ] Tag、Release 标题和程序版本一致
- [ ] 五个平台产物均已生成
- [ ] `SHA256SUMS` 已上传
- [ ] 新机器可以按 QUICKSTART 完成安装
- [ ] `amap install` 注册核验通过
- [ ] `amap index --tree-sitter` 可以完成基础索引
- [ ] Dashboard 可以启动并访问
- [ ] README 中不存在失效下载链接
- [ ] Release Notes 标明 Alpha/Beta/Stable 状态
- [ ] 未打包 Token、内部地址、测试项目或公司数据
