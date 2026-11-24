# Claude Code 采集插件安装指南

装好之后，你在 Claude Code 里改项目文件，插件会把当时的文件和这段对话记到一个单独的采集仓库。你自己项目里的 Git 记录不会被改动。

只聊天、没有改文件时，不会产生提交。

这个插件和 Codex 采集插件共用 `~/.trace-collector`。两个都装时，同一个项目的记录进同一个采集仓库。

## 开始之前

请确认这三件事已经就绪：

- 电脑上已安装 Claude Code，终端里能运行 `claude`，并且能正常对话。
- 电脑上已安装 Git。
- 本机已设置 Gitee 私人令牌，见下面「设置 Gitee 令牌」。不要把令牌写进插件目录或 `config.json`。

## 安装

1. 插件市场目录不要再移动。本仓库里是 `plugins/claude-code`。发给别人时，把这个目录解压到固定位置。Windows 例如 `D:\tools\claude_collector`。Linux 和 Mac 例如 `~/tools/claude_collector`。
2. 采集程序只放在市场目录下的 `claude-collector/bin`。仓库根目录的 `bin` 是 Codex 插件用的，Claude Code 不读取那里。

   Windows 使用现成的 `claude-collector/bin/collector.exe`，不用改权限。

   Linux 和 Mac 先准备可执行文件，再把 `claude-collector/hooks/hooks.json` 里三处 `collector.exe` 改成 `collector`：

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

3. 在 cmd 里执行，不要打开 Claude Code 会话。先进入市场目录，再注册并安装：

   ```bat
   cd /d D:\projects\ai\codex-addon\plugins\claude-code
   claude plugin marketplace add .\
   claude plugin install claude-collector@claude-collector-local
   ```

   单独一个 `.` 会被拒绝。Windows 上用 `.\`，或直接写绝对路径 `D:\projects\ai\codex-addon\plugins\claude-code`。

   市场目录如果换了位置，把 `cd` 改成那个目录。已经注册过这个市场时，第一条会提示已存在，继续执行第二条。

4. 再启动 Claude Code。插件从新会话开始生效。

## 填写配置

已经有 Codex 采集插件配置时，不用再写一份。

还没有配置时：

1. 在用户目录下新建文件夹 `.trace-collector`。
2. 把仓库根目录的 `config.example.json` 复制进去，重命名为 `config.json`。
3. 向使用者询问邮箱，把对方的回答写入 `email`。模版里这个值是空的，不要填成别人的地址。

三套系统的配置文件位置：

- Windows：`C:\Users\你的用户名\.trace-collector\config.json`
- Linux 和 Mac：`~/.trace-collector/config.json`

```json
{
  "email": "你的邮箱@gmail.com",
  "enterpriseNamespace": "https://公司Git地址/采集空间",
  "pushEveryN": 1,
  "consent": true,
  "maxFileBytes": 1073741824
}
```

`email` 必须留到询问使用者之后再填。不要把上面的示例换成作者或仓库里出现过的邮箱。

`maxFileBytes` 默认是 1GB。只有文件名后缀对不上、体积又超过这个值时，才按大文件处理。

采集仓库默认在配置文件旁边的 `repos` 目录里：

- Windows：`C:\Users\你的用户名\.trace-collector\repos`
- Linux 和 Mac：`~/.trace-collector/repos`

Windows 上如果 C 盘空间不够，在这份 `config.json` 里自行增加 `dataDir`，把插件数据改到其他盘。已有的采集仓库不会自动搬走。保存后新开一个 Claude Code 会话。

```json
"dataDir": "D:/trace-collector"
```

Linux 和 Mac 的例子：

```json
"dataDir": "/data/trace-collector"
```

## 设置 Gitee 令牌

Claude Code 从终端启动，读的是这个终端里的环境变量。

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

保存后新开一个终端，再启动 Claude Code。令牌必须是字面量，例如 `export GITEE_TOKEN="私人令牌"`。

## 确认已经生效

1. 用 Claude Code 打开一个普通项目文件夹。不要打开用户主目录。
2. 新开一个会话，让它改一个项目文件。
3. 这一轮结束后，用户目录下会出现采集记录。Windows 看 `C:\Users\你的用户名\.trace-collector\repos`，Linux 和 Mac 看 `~/.trace-collector/repos`。

有文件改动时，采集仓库里会多一次提交，并按配置推送到公司仓库。只打招呼、没有改文件时，这里不会多出提交。

## 使用时会看到什么

一轮回答结束后，采集在后台进行，不挡住你的下一句。

模型、数据集和压缩包不会送进仓库，只在清单里记录路径。密钥文件也不会送进仓库。
