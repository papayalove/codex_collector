---
name: install-codex-collector
description: >-
  在本机安装 Codex 采集插件：缺少 Git 或 Codex 命令行时由代理先装上，再固定插件目录、
  注册本地 marketplace、按 config.example.json 写入用户配置。Gitee 推送令牌已在程序内，更换时才写 GITEE_TOKEN_TRACE_ADDON。
  用户要求安装、重装或配置采集插件、codex-collector、Gitee 令牌时使用。
---

# 安装 Codex 采集插件

按下面的顺序做。不要把 Gitee 令牌写入插件目录或 `config.json`。采集 token 只写入用户目录配置里的 `traceToken`。不要改使用者项目仓库的 Git 远程或提交历史。

## 先拿到这些信息

缺任何一项就停下来问，不要猜，也不要沿用仓库或文档里出现过的地址：

1. 企业采集空间地址。`config.example.json` 里的 `enterpriseNamespace` 仍含「公司Git地址」这类占位，或为空时，必须问。
2. 不要向使用者索要 Gitee 私人令牌。采集程序里已经带了推送令牌。不要读写使用者自己的 `GITEE_TOKEN`。只有使用者明确要求更换时，才设置 `GITEE_TOKEN_TRACE_ADDON`；这个变量已经有值时不要覆盖。
3. 插件目录。当前目录同时有 `.codex-plugin/plugin.json`、`bin/`、`config.example.json` 时，就用当前目录。否则问一个以后不再移动的路径。Windows 可建议 `D:\tools\codex_collector`，Linux 和 Mac 可建议 `~/tools/codex_collector`。

向使用者询问邮箱和采集 token。两项都要有，分别写入 `email` 和 `traceToken`。缺任何一项就停下来问。不要把 token 或邮箱写进说明。

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
codex plugin add trace-collector@trace-collector
```

这个 marketplace 已经注册过时，继续执行 `codex plugin add`。不要换到另一个目录再注册一次。

## 配置

1. 建立用户目录下的 `.trace-collector`。
2. 还没有 `config.json` 时，把插件根目录的 `config.example.json` 复制过去并改名。已经有配置时，只改使用者这次明确要求的字段。
3. 用 JSON 解析后写入 `traceServer`、`email`、`traceToken` 和 `enterpriseNamespace`，保留其余字段。不要按 README 里的示例 JSON 重写整份文件。不要打印 token 或邮箱。
4. 只有使用者要求换数据盘时才增加 `dataDir`。Windows 写成 `D:/trace-collector` 这种正斜杠路径。Linux 和 Mac 例如 `/data/trace-collector`。

配置文件位置：

- Windows：`%USERPROFILE%\.trace-collector\config.json`
- Linux 和 Mac：`~/.trace-collector/config.json`

## 令牌

采集程序已经带了 Gitee 推送令牌。不要写入使用者自己的 `GITEE_TOKEN`。只有使用者明确要求更换时才设置 `GITEE_TOKEN_TRACE_ADDON`，检查和写入时都不要打印令牌的值。

`enterpriseNamespace` 写成 `https://gitee.com/组织名`，或企业版仓库组页面（例如 `https://gitee.pjlab.org.cn/L2/_source/groups/L2/agent-trace/trace-collector-repos/repos/`）。

## 交给使用者完成

这两步代理做不了，必须写进最终结果：

1. 完全退出 Codex 再打开。Windows 要退出托盘图标。Mac 要退出菜单栏里的 Codex，不要只关窗口。Linux 桌面端同样要完全退出。只用命令行时，新开一个终端。
2. 第一次安装或插件更新后允许钩子。桌面端在提问框点「审查钩子」，四个都勾着时点「全部允许」。命令行出现 `Hooks need review` 时选择 `Trust all and continue`，再确认一次。

不要为了验证去打开用户主目录，也不要擅自改使用者的项目文件。

## 怎样算做完

最终回复说明：插件目录、配置路径、`email` 和 `traceToken` 是否已写入、marketplace 是否注册成功，以及上面仍要使用者做的两步。不要复述令牌或邮箱。

`config.json` 缺少真实采集空间地址、邮箱或采集 token 时，安装不算完成。Gitee 推送令牌已经在采集程序里，不要再要求使用者配置推送令牌。环境里已经有 `GITEE_TOKEN_TRACE_ADDON` 时不要覆盖。会话开始时 token 校验失败则采集不会进行。仓库名用邮箱密文，不用 token。