import json
import tempfile
import unittest
from pathlib import Path

import oss_to_s3


def sample_config() -> dict:
    return {
        "intervalMinutes": 60,
        "mode": "copy",
        "compare": "size",
        "transfers": 4,
        "checkers": 2,
        "dryRun": True,
        "oss": {
            "accessKeyId": "oss-key",
            "accessKeySecret": "oss secret",
            "endpoint": "https://oss-cn-hangzhou.aliyuncs.com",
            "region": "cn-hangzhou",
            "bucket": "src-bucket",
            "prefix": "/traces/",
        },
        "s3": {
            "provider": "AWS",
            "accessKeyId": "s3-key",
            "secretAccessKey": "s3-secret",
            "region": "us-east-1",
            "endpoint": "",
            "bucket": "dst-bucket",
            "prefix": "incoming",
            "forcePathStyle": False,
        },
    }


class OssToS3Test(unittest.TestCase):
    def test_render_conf_hides_nothing_but_keeps_secrets_out_of_empty_endpoint(self) -> None:
        text = oss_to_s3.render_rclone_conf(sample_config())
        self.assertIn("provider = Alibaba", text)
        self.assertIn("endpoint = oss-cn-hangzhou.aliyuncs.com", text)
        self.assertIn('secret_access_key = "oss secret"', text)
        self.assertIn("[s3]", text)
        self.assertNotIn("endpoint = \n", text)
        s3 = text.split("[s3]", 1)[1]
        self.assertNotIn("endpoint =", s3)

    def test_build_argv_pulls_oss_to_s3(self) -> None:
        cfg = sample_config()
        oss_to_s3.validate(cfg)
        argv = oss_to_s3.build_argv(cfg, Path("C:/tmp/rclone.conf"))
        self.assertEqual(argv[1], "copy")
        self.assertEqual(argv[2], "oss:src-bucket/traces")
        self.assertEqual(argv[3], "s3:dst-bucket/incoming/")
        self.assertIn("--size-only", argv)
        self.assertIn("--dry-run", argv)
        self.assertNotIn("--no-check-certificate", argv)
        cfg["s3"]["endpoint"] = "ceph.example.com"
        argv = oss_to_s3.build_argv(cfg, Path("C:/tmp/rclone.conf"))
        self.assertIn("--no-check-certificate", argv)
        text = oss_to_s3.render_rclone_conf(cfg)
        self.assertIn("force_path_style = true", text.split("[s3]", 1)[1])
        self.assertIn("--config", argv)
        shown = oss_to_s3.public_argv(argv)
        self.assertNotIn("oss secret", shown)
        self.assertNotIn("rclone.conf", shown)

    def test_zero_byte_directory_marker_can_be_removed(self) -> None:
        entries = [
            {"Name": "test1", "Size": 0, "IsDir": False},
            {"Name": "other", "Size": 12, "IsDir": False},
        ]
        self.assertEqual(oss_to_s3.destination_marker_action(entries, "test1"), "delete")
        self.assertEqual(oss_to_s3.destination_marker_action([{"Name": "test1", "Size": 4, "IsDir": False}], "test1"), "blocked")
        self.assertEqual(oss_to_s3.destination_marker_action([{"Name": "test1", "IsDir": True}], "test1"), "ok")

    def test_missing_secret_is_rejected(self) -> None:
        cfg = sample_config()
        cfg["oss"]["accessKeySecret"] = "  "
        with self.assertRaises(oss_to_s3.ConfigError):
            oss_to_s3.validate(cfg)

    def test_load_config_roundtrip(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            path.write_text(json.dumps(sample_config()), encoding="utf-8")
            loaded = oss_to_s3.load_config(path)
            oss_to_s3.validate(loaded)
            self.assertEqual(loaded["s3"]["bucket"], "dst-bucket")


if __name__ == "__main__":
    unittest.main()
