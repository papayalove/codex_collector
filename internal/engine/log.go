package engine

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

var tokenLike = regexp.MustCompile(`(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]+`)
var authHeader = regexp.MustCompile(`(?i)authorization:\s*(?:bearer|basic)\s+\S+`)

func (e *Engine) reportCommit(branch, sha string, err error) {
	if err != nil {
		e.reportResult("提交失败", branch, err.Error())
		return
	}
	short := sha
	if len(short) > 12 {
		short = short[:12]
	}
	e.reportResult("提交成功", branch, short)
}

func (e *Engine) reportPush(branch string, err error) {
	if err != nil {
		e.reportResult("推送失败", branch, err.Error())
		return
	}
	e.reportResult("推送成功", branch, "")
}

func (e *Engine) reportResult(action, branch, detail string) {
	if e.Log == nil {
		return
	}
	parts := []string{"codex-collector:", action}
	if branch != "" {
		parts = append(parts, branch)
	}
	if detail != "" {
		parts = append(parts, briefLog(detail))
	}
	e.logMu.Lock()
	fmt.Fprintln(e.Log, strings.Join(parts, " "))
	e.logMu.Unlock()
}

func briefLog(s string) string {
	s = strings.Join(strings.Fields(redactSecrets(s)), " ")
	if utf8.RuneCountInString(s) > 160 {
		s = string([]rune(s)[:160])
	}
	return s
}

// recordDetail keeps the original error text for records.jsonl.
// Tokens are removed because this file is committed and pushed.
func recordDetail(s string) string {
	s = redactSecrets(s)
	const max = 4000
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func redactSecrets(s string) string {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITEE_TOKEN", "CODEUP_TOKEN", "YUNXIAO_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); len(v) >= 8 {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	s = tokenLike.ReplaceAllString(s, "***")
	s = authHeader.ReplaceAllString(s, "Authorization: ***")
	return s
}
