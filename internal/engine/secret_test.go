package engine

import "testing"

func TestSecretLinePatterns(t *testing.T) {
	hits := []string{
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"PuTTY-User-Key-File-2: ssh-ed25519",
		"key AKIAIOSFODNN7EXAMPLE",
		"ASIAIOSFODNN7EXAMPLE",
		"ghp_1234567890abcdefghijklmnopqrstuv",
		"github_pat_11AAAAAAA0123456789_abcdefghijklmnopqrstuvwxyz",
		"glpat-0123456789abcdefghij",
		"xoxb-1234567890-abcdefghij",
		"https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
		"sk_live_1234567890abcdef",
		"AIzaSyA1234567890abcdefghijklmnopqrstuvw",
		"sk-proj-abcdefghijklmnopqrstuvwxyz012345",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
		"hf_abcdefghijklmnopqrstuvwxyz",
		"npm_abcdefghijklmnopqrstuvwxyz",
		"SG.abcdefghijklmnopqrstuvwxyz.ABCDEF",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N",
		"Authorization: Bearer abcdefghijklmnop",
		"postgres://app:Sup3rSecret@db.internal:5432/app",
		`{"token":"abcdefghij"}`,
		"password = S3cretKey",
		"api key: abcdefghijk",
		"the secret is abcdefghijk",
		"我的密钥是 sk-proj-abcdefghijklmnopqrstuvwxyz",
		"秘钥：Abcd12345678",
		"令牌是 ghp_1234567890abcdefghijklmnopqrstuv",
		"密码是 Abcd123456",
		"密码123456",
		"密码 123456",
		"password123456",
		"pwd=123456",
		"访问密钥=AKIAiosfodnn7example",
		"ak=1wx92882xma",
		"sk=92kasd9wsas",
		"AK = 1wx92882xma",
		"sk:92kasd9wsas",
		`"ak":"1wx92882xma"`,
		"secret_key=1wx92882xma",
		"secretkey=1wx92882xma",
		"secret key=1wx92882xma",
		"secret-key=1wx92882xma",
		"SecretKey=1wx92882xma",
		"SECRET_KEY:1wx92882xma",
		`"secret_key":"1wx92882xma"`,
		"secret_key is 1wx92882xma",
		"secretkey is 1wx92882xma",
		"the secret key is 1wx92882xma",
		"secret_key 1wx92882xma",
		"secretkey 1wx92882xma",
		"secret key 1wx92882xma",
		"secret_key是1wx92882xma",
		"secret key是92kasd9wsas",
	}
	for _, line := range hits {
		if !secretLine.MatchString(line) {
			t.Errorf("missed %q", line)
		}
		got := string(redact([]byte(line)))
		if got != "[REDACTED]" {
			t.Errorf("redact %q => %q", line, got)
		}
	}

	misses := []string{
		"hello world",
		"token",
		"password is short",
		"这是普通句子，没有密钥",
		"密钥是太短",
		"请把密码改掉",
		"密码12345",
		"https://example.com/path",
		"ask=abcdefghij",
		"ak=short",
		"task=abcdefghij",
		"rotate the secret key",
		"the secret key is short",
		"secret key management",
	}
	for _, line := range misses {
		if secretLine.MatchString(line) {
			t.Errorf("false positive %q", line)
		}
	}
}
