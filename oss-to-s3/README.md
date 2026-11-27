# OSS 拉到 S3

用 rclone 把阿里云 OSS 上的对象拉到 S3。密钥放在本目录的 `config.json`，不要写进脚本。

## 准备

1. 安装 [rclone](https://rclone.org/downloads/)，并确认命令行能执行 `rclone version`。
2. 复制 `config.example.json` 为 `config.json`，填入 OSS 和 S3 的密钥、endpoint、bucket。
3. `prefix` 留空表示整桶。目标 prefix 按目录写入。如果控制台里的目录其实是一个 0 字节占位对象，脚本会先去掉这个占位再上传。有内容的同名文件不会删除。`mode` 用 `copy` 只拉取新对象；改成 `sync` 时，S3 上源端已经没有的对象会被删掉。

## 运行

```text
python oss_to_s3.py
python oss_to_s3.py --install
python oss_to_s3.py --uninstall
```

`--install` 读取 `intervalMinutes`。Windows 上注册当前用户的计划任务 `oss-to-s3`，Linux 上写入当前用户的 crontab。日志在 `logs/oss-to-s3.log`。上一次还在跑时，新的一次会直接跳过。
