# Claude Code 采集插件安装指南

装好之后，你在 Claude Code 里改项目文件，插件会把当时的文件和这段对话记到一个单独的采集仓库。你自己项目里的 Git 记录不会被改动。

只聊天、没有改文件时，不会产生提交。

这个插件和 Codex 采集插件共用同一套配置和采集仓库，目录都是 `~/.trace-collector`。两个都装的话，同一个项目在两边产生的记录会进同一个采集仓库。

## 开始之前

请确认这三件事已经就绪：

- 电脑上已安装 Claude Code，终端里能运行 `claude`，并且能正常对话。
- 电脑上已安装 Git。
- 本机已设置 Gitee 私人令牌，见下面「设置 Gitee 令牌」。不要把令牌写进插件目录或 `config.json`。

## 安装

1. 把插件文件夹解压到一个固定位置，以后不要移动。macOS 和 Linux 例如 `~/tools/claude_collector`。
2. 采集程序就在本目录的 `bin` 里，不要用仓库根目录那个 Codex 用的 `bin`。Linux 和 Mac 添加执行权限：

   ```bash
   cd ./claude_collector/claude-collector
   # Linux
   chmod +x bin/collector

   # Mac（Apple 芯片）：
   cp bin/collector-darwin-arm64 bin/collector
   chmod +x bin/collector

   # Mac（Intel）：
   cp bin/collector-darwin-amd64 bin/collector
   chmod +x bin/collector
   ```

   Windows 跳过这一步，直接用 `bin/collector.exe`。

3. 在 cmd 里执行，不要打开 Claude Code 会话。先进入解压出来的市场目录，再注册并安装：

   ```bat
   cd /d D:\tools\claude_collector
   claude plugin marketplace add .\
   claude plugin install claude-collector@claude-collector-local
   ```

   单独一个 `.` 会被拒绝。Windows 上用 `.\`，或直接写市场目录的绝对路径。

   已经注册过这个市场时，第一条会提示已存在，继续执行第二条。

4. 再启动 Claude Code。插件从新会话开始生效。

## 填写配置

1. 在用户目录下新建文件夹 `.trace-collector`。
2. 把插件目录里的 `config.example.json` 复制进去，重命名为 `config.json`。
3. 向使用者询问邮箱，把对方的回答写入 `email`。模版里这个值是空的，不要填成别人的地址。

配置文件位置：

- Windows：`C:\Users\你的用户名\.trace-collector\config.json`
- Linux 和 Mac：`~/.trace-collector/config.json`

```json
{
  "email": "你的邮箱",
  "enterpriseNamespace": "https://公司Git地址/采集空间",
  "pushEveryN": 2,
  "consent": true,
  "maxFileBytes": 1073741824
}
```

`maxFileBytes` 默认是 1GB。只有文件名后缀对不上、体积又超过这个值时，才按大文件处理。

采集仓库默认在配置文件旁边的 `repos` 目录里。

## 设置 Gitee 令牌

Claude Code 从终端启动，会继承终端的环境变量。

`enterpriseNamespace` 写成 `https://gitee.com/组织名`。企业版可以直接填仓库组页面，例如 `https://gitee.pjlab.org.cn/L2/_source/groups/L2/agent-trace/trace-collector-repos/repos/`，采集器会收成子组 `https://gitee.pjlab.org.cn/L2/agent-trace/trace-collector-repos/repos`。私人令牌勾选 Git http push 和代码库。不要把令牌写进插件目录或 `config.json`。

### Windows

在 PowerShell 里执行。执行后重新打开终端，再启动 Claude Code。已经开着的命令行窗口不会读到这次写入的令牌。

```powershell
setx GITEE_TOKEN "私人令牌"
```

### Linux 和 Mac

```bash
# Linux
echo 'export GITEE_TOKEN="私人令牌"' >> ~/.bashrc

# Mac
echo 'export GITEE_TOKEN="私人令牌"' >> ~/.zshrc
```

保存后重新打开终端，再启动 Claude Code。令牌必须是字面量，例如 `export GITEE_TOKEN="私人令牌"`。改了令牌之后，新开一个 Claude Code 会话即可。

## Windows 用户

当前钩子命令调用的是 `bin/collector.exe`，Windows 上不用再改 `hooks.json`。Claude Code 的 Windows 原生版还处在实验阶段。

Linux 和 Mac 安装时，把 `hooks/hooks.json` 里三处 `collector.exe` 改成 `collector`，并按上面的步骤准备好对应的可执行文件。

## 确认已经生效

1. 用 Claude Code 打开一个普通项目文件夹。不要打开用户主目录。
2. 新开一个会话，让它改一个项目文件。
3. 这一轮结束后，看采集目录。Windows 看 `C:\Users\你的用户名\.trace-collector\repos`，Linux 和 Mac 看 `~/.trace-collector/repos`。

有文件改动时，采集仓库里会多一次提交，并按配置推送到公司仓库。只打招呼、没有改文件时，这里不会多出提交。

## 使用时会看到什么

一轮回答结束后，采集在后台进行，不挡住你的下一句。

模型、数据集和压缩包不会送进仓库，只在清单里记录路径。密钥文件也不会送进仓库。
