"""把阿里云 OSS 上的对象用 rclone 拉到 S3。

密钥只从配置文件读取，不写进命令行参数，也不打印到日志。
默认同一次只拉取，不删除 S3 上多出来的对象。mode 设为 sync 时才会让目标与来源对齐。

用法：
    copy config.example.json 为 config.json 并填入密钥
    python oss_to_s3.py
    python oss_to_s3.py --install
    python oss_to_s3.py --uninstall
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_CONFIG = SCRIPT_DIR / "config.json"
TASK_NAME = "oss-to-s3"
LOG_DIR = SCRIPT_DIR / "logs"
LOCK_PATH = SCRIPT_DIR / ".run" / "oss-to-s3.lock"
MAX_LOG_BYTES = 5 * 1024 * 1024


class ConfigError(Exception):
    pass


def load_config(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise ConfigError(f"找不到配置文件：{path}")
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ConfigError(f"配置文件不是合法 JSON：{exc}") from exc
    if not isinstance(data, dict):
        raise ConfigError("配置文件的顶层必须是对象")
    return data


def _section(cfg: dict[str, Any], name: str) -> dict[str, Any]:
    value = cfg.get(name)
    if not isinstance(value, dict):
        raise ConfigError(f"配置缺少对象字段 {name}")
    return value


def _required(section: dict[str, Any], key: str, label: str) -> str:
    value = section.get(key, "")
    if not isinstance(value, str) or not value.strip():
        raise ConfigError(f"配置缺少 {label}")
    if any(ch in value for ch in "\r\n"):
        raise ConfigError(f"{label} 不能包含换行")
    return value.strip()


def _optional(section: dict[str, Any], key: str, default: str = "") -> str:
    value = section.get(key, default)
    if value is None:
        return default
    if not isinstance(value, str):
        raise ConfigError(f"{key} 必须是字符串")
    if any(ch in value for ch in "\r\n"):
        raise ConfigError(f"{key} 不能包含换行")
    return value.strip()


def _positive_int(cfg: dict[str, Any], key: str, default: int) -> int:
    value = cfg.get(key, default)
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ConfigError(f"{key} 必须是正整数")
    return value


def validate(cfg: dict[str, Any]) -> None:
    oss = _section(cfg, "oss")
    s3 = _section(cfg, "s3")
    _required(oss, "accessKeyId", "oss.accessKeyId")
    _required(oss, "accessKeySecret", "oss.accessKeySecret")
    _required(oss, "endpoint", "oss.endpoint")
    _required(oss, "bucket", "oss.bucket")
    _required(s3, "accessKeyId", "s3.accessKeyId")
    _required(s3, "secretAccessKey", "s3.secretAccessKey")
    _required(s3, "bucket", "s3.bucket")
    provider = _optional(s3, "provider", "AWS") or "AWS"
    if not _optional(s3, "region") and not _optional(s3, "endpoint") and provider.upper() == "AWS":
        raise ConfigError("s3.region 或 s3.endpoint 至少填一个")
    mode = _optional(cfg, "mode", "copy") or "copy"
    if mode not in ("copy", "sync"):
        raise ConfigError("mode 只能是 copy 或 sync")
    compare = _optional(cfg, "compare", "size") or "size"
    if compare not in ("size", "checksum", "modtime"):
        raise ConfigError("compare 只能是 size、checksum 或 modtime")
    _positive_int(cfg, "intervalMinutes", 60)
    _positive_int(cfg, "transfers", 8)
    _positive_int(cfg, "checkers", 8)
    if "dryRun" in cfg and not isinstance(cfg["dryRun"], bool):
        raise ConfigError("dryRun 必须是布尔值")
    if "forcePathStyle" in s3 and not isinstance(s3["forcePathStyle"], bool):
        raise ConfigError("s3.forcePathStyle 必须是布尔值")


def endpoint_host(value: str) -> str:
    host = value.strip()
    lower = host.lower()
    for prefix in ("https://", "http://"):
        if lower.startswith(prefix):
            host = host[len(prefix):]
            break
    return host.strip().strip("/")


def ini_value(value: str) -> str:
    if value == "" or value.strip() != value or any(ch in value for ch in " #;\"'"):
        escaped = value.replace("\\", "\\\\").replace('"', '\\"')
        return f'"{escaped}"'
    return value


def _remote_lines(name: str, fields: list[tuple[str, str]]) -> list[str]:
    lines = [f"[{name}]", "type = s3"]
    for key, value in fields:
        if value == "":
            continue
        lines.append(f"{key} = {ini_value(value)}")
    return lines


def render_rclone_conf(cfg: dict[str, Any]) -> str:
    oss = cfg["oss"]
    s3 = cfg["s3"]
    oss_fields = [
        ("provider", "Alibaba"),
        ("env_auth", "false"),
        ("access_key_id", oss["accessKeyId"].strip()),
        ("secret_access_key", oss["accessKeySecret"].strip()),
        ("endpoint", endpoint_host(oss["endpoint"])),
        ("region", _optional(oss, "region")),
        ("acl", "private"),
    ]
    provider = _optional(s3, "provider", "AWS") or "AWS"
    s3_fields = [
        ("provider", provider),
        ("env_auth", "false"),
        ("access_key_id", s3["accessKeyId"].strip()),
        ("secret_access_key", s3["secretAccessKey"].strip()),
        ("region", _optional(s3, "region")),
        ("endpoint", endpoint_host(_optional(s3, "endpoint"))),
        ("acl", "private"),
    ]
    if s3.get("forcePathStyle") is True:
        s3_fields.append(("force_path_style", "true"))
    lines = _remote_lines("oss", oss_fields) + [""] + _remote_lines("s3", s3_fields) + [""]
    return "\n".join(lines)


def remote_spec(bucket: str, prefix: str) -> str:
    bucket = bucket.strip().strip("/")
    prefix = prefix.strip().strip("/")
    if prefix:
        return f"{bucket}/{prefix}"
    return bucket


def destination_marker_action(entries: list[dict[str, Any]], leaf: str) -> str:
    """Decide what to do with an S3 key that has the same name as the destination directory.

    A console folder is often a zero-byte object. rclone refuses to copy into that
    object, so the empty marker can be removed. A non-empty object stays in place.
    """
    action = "ok"
    for item in entries:
        if not isinstance(item, dict):
            continue
        name = str(item.get("Name") or item.get("Path") or "").strip("/")
        if name != leaf:
            continue
        if item.get("IsDir") is True:
            continue
        size = item.get("Size") or 0
        try:
            size = int(size)
        except (TypeError, ValueError):
            size = 0
        if size > 0:
            return "blocked"
        action = "delete"
    return action


def build_argv(cfg: dict[str, Any], rclone_config: Path) -> list[str]:
    mode = _optional(cfg, "mode", "copy") or "copy"
    compare = _optional(cfg, "compare", "size") or "size"
    rclone = _optional(cfg, "rclonePath", "rclone") or "rclone"
    source = "oss:" + remote_spec(cfg["oss"]["bucket"], _optional(cfg["oss"], "prefix"))
    dest = "s3:" + remote_spec(cfg["s3"]["bucket"], _optional(cfg["s3"], "prefix")) + "/"
    argv = [
        rclone,
        mode,
        source,
        dest,
        "--config",
        str(rclone_config),
        "--transfers",
        str(_positive_int(cfg, "transfers", 8)),
        "--checkers",
        str(_positive_int(cfg, "checkers", 8)),
        "--stats",
        "30s",
        "--stats-one-line",
    ]
    if compare == "size":
        argv.append("--size-only")
    elif compare == "checksum":
        argv.append("--checksum")
    if cfg.get("dryRun") is True:
        argv.append("--dry-run")
    return argv


def public_argv(argv: list[str]) -> str:
    shown = []
    skip_next = False
    for arg in argv:
        if skip_next:
            shown.append("<config>")
            skip_next = False
            continue
        if arg == "--config":
            shown.append(arg)
            skip_next = True
            continue
        shown.append(arg)
    return " ".join(shown)


def log_path() -> Path:
    LOG_DIR.mkdir(parents=True, exist_ok=True)
    path = LOG_DIR / "oss-to-s3.log"
    if path.is_file() and path.stat().st_size > MAX_LOG_BYTES:
        backup = LOG_DIR / "oss-to-s3.log.1"
        if backup.exists():
            backup.unlink()
        path.replace(backup)
    return path


def write_log(message: str) -> None:
    line = time.strftime("%Y-%m-%d %H:%M:%S") + " " + message
    print(line, flush=True)
    with log_path().open("a", encoding="utf-8") as handle:
        handle.write(line + "\n")


def pid_alive(pid: int) -> bool:
    if pid <= 0:
        return False
    if os.name == "nt":
        import ctypes

        handle = ctypes.windll.kernel32.OpenProcess(0x1000, False, pid)
        if not handle:
            return False
        ctypes.windll.kernel32.CloseHandle(handle)
        return True
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    return True


def acquire_lock() -> bool:
    LOCK_PATH.parent.mkdir(parents=True, exist_ok=True)
    flags = os.O_CREAT | os.O_EXCL | os.O_WRONLY
    try:
        fd = os.open(LOCK_PATH, flags)
    except FileExistsError:
        try:
            existing = int(LOCK_PATH.read_text(encoding="utf-8").strip() or "0")
        except ValueError:
            existing = 0
        if pid_alive(existing):
            return False
        LOCK_PATH.unlink(missing_ok=True)
        fd = os.open(LOCK_PATH, flags)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(str(os.getpid()))
    return True


def release_lock() -> None:
    try:
        if LOCK_PATH.is_file() and LOCK_PATH.read_text(encoding="utf-8").strip() == str(os.getpid()):
            LOCK_PATH.unlink(missing_ok=True)
    except OSError:
        pass


def write_rclone_conf(text: str) -> Path:
    directory = SCRIPT_DIR / ".run"
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"rclone-{os.getpid()}.conf"
    path.write_text(text, encoding="utf-8")
    try:
        os.chmod(path, 0o600)
    except OSError:
        pass
    return path


def _rclone_json(cfg: dict[str, Any], conf_path: Path, argv: list[str]) -> list[dict[str, Any]]:
    rclone = _optional(cfg, "rclonePath", "rclone") or "rclone"
    completed = subprocess.run([rclone, *argv, "--config", str(conf_path)], check=False, capture_output=True, text=True)
    if completed.returncode != 0 or not completed.stdout.strip():
        return []
    try:
        data = json.loads(completed.stdout)
    except json.JSONDecodeError:
        return []
    if not isinstance(data, list):
        return []
    return [item for item in data if isinstance(item, dict)]


def prepare_destination(cfg: dict[str, Any], conf_path: Path) -> None:
    prefix = _optional(cfg["s3"], "prefix")
    if not prefix:
        return
    leaf = prefix.strip("/").rsplit("/", 1)[-1]
    parent = prefix.strip("/").rsplit("/", 1)[0] if "/" in prefix.strip("/") else ""
    parent_remote = "s3:" + remote_spec(cfg["s3"]["bucket"], parent)
    entries = _rclone_json(cfg, conf_path, ["lsjson", parent_remote, "--max-depth", "1"])
    action = destination_marker_action(entries, leaf)
    if action == "ok":
        return
    if action == "blocked":
        raise ConfigError(f"S3 目标 {prefix} 是一个有内容的文件，不能当作目录写入")
    rclone = _optional(cfg, "rclonePath", "rclone") or "rclone"
    marker = "s3:" + remote_spec(cfg["s3"]["bucket"], prefix)
    write_log(f"去掉 S3 目录占位对象 {prefix}")
    subprocess.run([rclone, "deletefile", marker, "--config", str(conf_path)], check=False)


def run_once(config_path: Path) -> int:
    cfg = load_config(config_path)
    validate(cfg)
    rclone = _optional(cfg, "rclonePath", "rclone") or "rclone"
    if shutil.which(rclone) is None and not Path(rclone).is_file():
        raise ConfigError(f"找不到 rclone：{rclone}")
    if not acquire_lock():
        write_log("已有拉取任务在运行，本次跳过")
        return 0
    conf_path: Path | None = None
    started = time.monotonic()
    try:
        conf_path = write_rclone_conf(render_rclone_conf(cfg))
        prepare_destination(cfg, conf_path)
        argv = build_argv(cfg, conf_path)
        write_log("开始拉取 " + public_argv(argv))
        completed = subprocess.run(argv, check=False)
        elapsed = int(time.monotonic() - started)
        if completed.returncode != 0:
            write_log(f"拉取失败，退出码 {completed.returncode}，用时 {elapsed} 秒")
            return completed.returncode or 1
        write_log(f"拉取完成，用时 {elapsed} 秒")
        return 0
    finally:
        if conf_path is not None:
            conf_path.unlink(missing_ok=True)
        release_lock()


def schedule_parts(minutes: int) -> tuple[str, str]:
    if minutes % 1440 == 0:
        return "DAILY", str(max(1, minutes // 1440))
    if minutes % 60 == 0:
        return "HOURLY", str(minutes // 60)
    if minutes > 1439:
        raise ConfigError("intervalMinutes 不能大于 1439，除非它是 60 或 1440 的整数倍")
    return "MINUTE", str(minutes)


def task_command(config_path: Path) -> str:
    return f'"{sys.executable}" "{SCRIPT_DIR / "oss_to_s3.py"}" --config "{config_path}"'


def install_schedule(config_path: Path) -> int:
    cfg = load_config(config_path)
    validate(cfg)
    minutes = _positive_int(cfg, "intervalMinutes", 60)
    if os.name == "nt":
        schedule, modifier = schedule_parts(minutes)
        argv = [
            "schtasks",
            "/Create",
            "/F",
            "/SC",
            schedule,
            "/MO",
            modifier,
            "/TN",
            TASK_NAME,
            "/TR",
            task_command(config_path),
            "/RL",
            "LIMITED",
        ]
        completed = subprocess.run(argv, check=False)
        if completed.returncode != 0:
            write_log(f"注册定时任务失败，退出码 {completed.returncode}")
            return completed.returncode or 1
        write_log(f"已注册 Windows 定时任务 {TASK_NAME}，每 {minutes} 分钟拉取一次")
        return 0
    line = f"*/{minutes} * * * * {task_command(config_path)}"
    if minutes % 60 == 0 and minutes >= 60:
        hour_step = minutes // 60
        if hour_step == 1:
            line = f"0 * * * * {task_command(config_path)}"
        elif 24 % hour_step == 0:
            line = f"0 */{hour_step} * * * {task_command(config_path)}"
    elif minutes >= 60:
        raise ConfigError("Linux cron 只支持不超过 59 分钟，或 60 分钟的整数倍")
    existing = subprocess.run(["crontab", "-l"], check=False, capture_output=True, text=True)
    rows = [] if existing.returncode != 0 else existing.stdout.splitlines()
    rows = [row for row in rows if TASK_NAME not in row and "oss_to_s3.py" not in row]
    rows.append(f"{line} # {TASK_NAME}")
    updated = subprocess.run(["crontab", "-"], input="\n".join(rows) + "\n", text=True, check=False)
    if updated.returncode != 0:
        write_log("写入 crontab 失败")
        return updated.returncode or 1
    write_log(f"已写入 crontab，每 {minutes} 分钟拉取一次")
    return 0


def uninstall_schedule() -> int:
    if os.name == "nt":
        completed = subprocess.run(["schtasks", "/Delete", "/F", "/TN", TASK_NAME], check=False)
        if completed.returncode != 0:
            write_log(f"删除定时任务失败，退出码 {completed.returncode}")
            return completed.returncode or 1
        write_log(f"已删除 Windows 定时任务 {TASK_NAME}")
        return 0
    existing = subprocess.run(["crontab", "-l"], check=False, capture_output=True, text=True)
    if existing.returncode != 0:
        write_log("当前用户没有 crontab")
        return 0
    rows = [row for row in existing.stdout.splitlines() if TASK_NAME not in row and "oss_to_s3.py" not in row]
    updated = subprocess.run(["crontab", "-"], input="\n".join(rows) + "\n", text=True, check=False)
    if updated.returncode != 0:
        write_log("更新 crontab 失败")
        return updated.returncode or 1
    write_log("已从 crontab 移除拉取任务")
    return 0


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="用 rclone 定时把 OSS 对象拉到 S3")
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG, help="配置文件路径")
    parser.add_argument("--install", action="store_true", help="按配置里的间隔注册定时任务")
    parser.add_argument("--uninstall", action="store_true", help="删除定时任务")
    args = parser.parse_args(argv)
    if args.install and args.uninstall:
        parser.error("--install 和 --uninstall 不能同时使用")
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        if args.uninstall:
            return uninstall_schedule()
        if args.install:
            return install_schedule(args.config)
        return run_once(args.config)
    except ConfigError as exc:
        write_log(str(exc))
        return 2


if __name__ == "__main__":
    sys.exit(main())
