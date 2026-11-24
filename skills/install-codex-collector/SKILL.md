---
name: install-codex-collector
description: >-
  在本机安装 Codex 采集插件：缺少 Git 或 Codex 命令行时由代理先装上，再固定插件目录、
  注册本地 marketplace、按 config.example.json 写入用户配置，并设置 GITEE_TOKEN。
  用户要求安装、重装或配置采集插件、codex-collector、Gitee 令牌时使用。
---

# 安装 Codex 采集插件

按下面的顺序做。不要把令牌写入插件目录或 `config.json`。不要改使用者项目仓库的 Git 远程或提交历史。

## 先拿到这些信息

缺任何一项就停下来问，不要猜，也不要沿用仓库或文档里出现过的邮箱和地址：

1. 使用者邮箱。只写入对方这次的回答。
2. 企业采集空间地址。`config.example.json` 里的 `enterpriseNamespace` 仍含「公司Git地址」这类占位，或为空时，必须问。
3. Gitee 私人令牌。用户环境或 shell 配置里已经有字面量 `GITEE_TOKEN` 时不要覆盖，除非使用者明确要求更换。
4. 插件目录。当前目录同时有 `.codex-plugin/plugin.json`、`bin/`、`config.example.json` 时，就用当前目录。否则问一个以后不再移动的路径。Windows 可建议 `D:\tools\codex_collector`，Linux 和 Mac 可建议 `~/tools/codex_collector`。

只有使用者说系统盘空间不够时，再问 `dataDir`。已有采集仓库不会自动搬走。

## 准备 Git 和 Codex

`git` 和 `codex` 已经能运行就跳过。缺哪个就由代理安装哪个，不要把这一步交给使用者。装完后刷新当前终端的 PATH，再确认命令可用。确认失败就停，并说明失败的命令。

git按系统安装：

- Windows：`winget install --id Git.Git -e --accept-package-agreements --accept-source-agreements`
- Mac：已有 Homebrew 时执行 `brew install git`。没有 Homebrew 时执行 `xcode-select --install`，这一步要等使用者在弹出的窗口里确认。
- Linux：用本机包管理器安装 `git`，`apt-get`、`dnf`、`pacman` 里有哪个用哪个。需要管理员密码时，向使用者要一次授权。

Codex 命令行用 npm 安装：

```bash
npm install -g @openai/codex
```

## 安装

1. 插件目录一旦注册，就不要再移动。
2. 按系统准备采集程序，只做当前系统那一步：
  - Windows：使用现成的 `bin/collector.exe`。
  - Linux：`chmod +x bin/collector`。
  - Mac（Apple 芯片）：`cp bin/collector-darwin-arm64 bin/collector`，然后 `chmod +x bin/collector`。
  - Mac（Intel）：`cp bin/collector-darwin-amd64 bin/collector`，然后 `chmod +x bin/collector`。
3. 在插件根目录执行：

```bash
codex plugin marketplace add .
codex plugin add codex-collector@codex-collector-local
```

这个 marketplace 已经注册过时，继续执行 `codex plugin add`。不要换到另一个目录再注册一次。

## 配置

1. 建立用户目录下的 `.trace-collector`。
2. 还没有 `config.json` 时，把插件根目录的 `config.example.json` 复制过去并改名。已经有配置时，只改使用者这次明确要求的字段。
3. 用 JSON 解析后写入 `email` 和 `enterpriseNamespace`，保留其余字段。不要按 README 里的示例 JSON 重写整份文件。
4. 只有使用者要求换数据盘时才增加 `dataDir`。Windows 写成 `D:/trace-collector` 这种正斜杠路径。Linux 和 Mac 例如 `/data/trace-collector`。

配置文件位置：

- Windows：`%USERPROFILE%\.trace-collector\config.json`
- Linux 和 Mac：`~/.trace-collector/config.json`

## 令牌

先检查是否已经设置，检查时不要打印令牌的值。采集仓库在 Gitee 上，使用 `GITEE_TOKEN`。私人令牌勾选 Git http push 和代码库。

- Windows：看用户级环境变量 `GITEE_TOKEN`。没有则在 PowerShell 执行 `setx GITEE_TOKEN "私人令牌"`。`setx` 不会改已经打开的窗口，装完后要新开终端，并完全退出 Codex 再打开。
- Linux：`~/.bashrc` 里没有字面量 `export GITEE_TOKEN="私人令牌"` 时，追加这一行。已有就不要再追加。
- Mac：按同样规则写入 `~/.zshrc`。

`enterpriseNamespace` 写成 `https://gitee.com/组织名`，或企业版仓库组页面（例如 `https://gitee.pjlab.org.cn/L2/_source/groups/L2/agent-trace/trace-collector-repos/repos/`）。

`export GITEE_TOKEN=$OTHER` 这种间接赋值不算已设置。

## 交给使用者完成

这两步代理做不了，必须写进最终结果：

1. 完全退出 Codex 再打开。Windows 要退出托盘图标。Mac 要退出菜单栏里的 Codex，不要只关窗口。Linux 桌面端同样要完全退出。只用命令行时，新开一个终端。
2. 第一次安装或插件更新后允许钩子。桌面端在提问框点「审查钩子」，四个都勾着时点「全部允许」。命令行出现 `Hooks need review` 时选择 `Trust all and continue`，再确认一次。

不要为了验证去打开用户主目录，也不要擅自改使用者的项目文件。

## 怎样算做完

最终回复说明：插件目录、配置路径、邮箱是否已写入、令牌是否已设置、marketplace 是否注册成功，以及上面仍要使用者做的两步。不要复述令牌。

`config.json` 缺少邮箱或真实采集空间地址，或者令牌没有设上时，安装不算完成。