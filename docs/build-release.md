# 构建与打包

本文只讲“怎么把这个仓库变成可分发的二进制”，不涉及运行时行为。
发布配置的唯一事实来源是根目录的 `.goreleaser.yml`；这里是它的使用说明和本机踩过的坑。

## 1. 前置条件

| 项 | 要求 | 说明 |
|---|---|---|
| Go | 1.25+ | `go.mod` 声明 `go 1.25` |
| `../agentcore` | 必须存在 | 本地构建需要，见下方 §1.1 |
| goreleaser | v2 | 跨平台打包用；只做本机构建可以不装 |

安装 goreleaser（任选其一）：

```bash
go install github.com/goreleaser/goreleaser/v2@latest   # 装到 $GOPATH/bin
brew install goreleaser                                  # macOS
```

### 1.1 go.work 与 agentcore

`internal/agents/ctxpack` 用到 `corecontext.FindCutPoint`，这个函数只在 agentcore
main 分支上有，不在 `go.mod` 钉的 v1.8.2 里。所以**本地构建必须有 go.work 指向
同级的 agentcore 检出**（`go.work` 已被 gitignore，缺了要自己建）：

```bash
git clone https://github.com/voocel/agentcore ../agentcore
cat > go.work <<'EOF'
go 1.25.5

use (
	.
	../agentcore
)
EOF
```

本地不要设 `GOWORK=off`。CI 设了 `GOWORK=off`，所以在 agentcore 打出新 tag、
`go.mod` 跟着升上去之前，CI 会在这一点上挂——这是已知状态，不是构建脚本的问题。
升 agentcore 前先跑 `go test ./internal/agents -run 'TestContract_'`。

Docker 构建走的是 `GOWORK=off`（见 `Dockerfile`），因此同样受这条限制。

## 2. 本机单平台构建

```bash
go build ./cmd/ainovel-cli          # 产出 ./ainovel-cli(.exe)
go run ./cmd/ainovel-cli            # 直接跑 TUI
```

这条路径不带 version/commit 注入，`ainovel-cli --version` 会显示开发版。

## 3. 全平台快照打包（日常出包走这条）

```bash
goreleaser release --snapshot --clean
```

- `--snapshot`：不需要 git tag，也不推任何东西。本仓库当前不是 git 仓库，
  goreleaser 会打印 “accepting to run without a git repository because this is a
  snapshot” 并把版本记成 `0.0.0-SNAPSHOT-none`。
- `--clean`：先清空 `dist/` 再构建。**上一次的产物会被删掉**，要留就先备份。
- 跑之前会执行 `.goreleaser.yml` 里的 before hooks：`go mod tidy` → `go vet ./...`
  → `go test -count=1 ./...`。任一步失败就不出包，所以打包本身就是一次完整验证。
- 全量约 1 分钟（6 个平台并行编译占大头）。

产物落在 `dist/`：

```
ainovel-cli_<version>_Darwin_arm64.tar.gz
ainovel-cli_<version>_Darwin_x86_64.tar.gz
ainovel-cli_<version>_Linux_arm64.tar.gz
ainovel-cli_<version>_Linux_x86_64.tar.gz
ainovel-cli_<version>_Windows_arm64.zip
ainovel-cli_<version>_Windows_x86_64.zip
ainovel-cli_checksums.txt          # sha256，install.sh 会校验
```

每个包里是 `ainovel-cli` 二进制 + `README.md` + `LICENSE`。assets/ 下的提示词、
参考资料、风格文件都是 `go:embed` 进二进制的，不单独打包。

只想验证配置、不想等编译：

```bash
goreleaser check                    # 校验 .goreleaser.yml
goreleaser build --snapshot --clean --single-target   # 只编当前平台
```

## 4. 正式发布

正式发布需要真实 git tag 和 GitHub 凭证，`.goreleaser.yml` 的 `release.github`
指向 `voocel/ainovel-cli`：

```bash
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
GITHUB_TOKEN=<token> goreleaser release --clean
```

版本号通过 ldflags 注入 `main.version` / `main.commit` / `main.date`。
发布后 `scripts/install.sh` 和客户端的更新检查（`internal/version`，读 GitHub
Releases 公开接口）就能拿到新版本。

## 5. Docker

```bash
docker build -t ainovel-cli .
docker run --rm -it \
  -v "$PWD/book":/workspace \
  -v "$HOME/.ainovel":/root/.ainovel \
  ainovel-cli
```

一本书一个 `/workspace` 挂载点（`output/novel` 在容器里的工作目录下），
配置目录可以多本书共用。`docker-compose.yml` 是同一套挂载的现成写法。

## 6. 踩过的坑

- **`config.example.jsonc` 有两份**：根目录一份、`internal/bootstrap/` 一份（后者被
  embed）。`TestExampleConfigIsValidAndSelfConsistent` 会比对两者，改了根目录那份
  必须 `cp config.example.jsonc internal/bootstrap/config.example.jsonc`，否则 before
  hook 的测试直接把打包拦下来。
- **`gofmt -l .` 必须是空输出**，CI 以此为准，goreleaser 的 hook 里没有这一步，
  自己记得跑。
- **`go mod tidy` 不会因为 go.work 改动 go.mod**：hook 里那一步跑完 `go.mod`/`go.sum`
  的时间戳不变，这是预期的。
- **`go test -race` 在部分 Windows 机器上跑不了**：所有包都以 `0xc0000139`
  (STATUS_ENTRYPOINT_NOT_FOUND) 失败，包括没改过的包。那是 race runtime 缺 DLL，
  与代码无关，留给 CI 的 race job。打包流程本身不跑 race。
