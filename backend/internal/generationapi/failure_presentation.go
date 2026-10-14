package generationapi

import (
	"regexp"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

var statementPresentationPattern = regexp.MustCompile(`(?i)\b(?:title|narrative) (?:contains normative or meta term|must not introduce normative operators)`)
var statementForbiddenTermPattern = regexp.MustCompile(`(?i)\b(?:title|narrative) contains normative or meta term "([^"]+)"`)

func statementPresentationFailure(diagnostic string) *domain.GenerationFailure {
	if !statementPresentationPattern.MatchString(diagnostic) {
		return nil
	}
	retryable := false
	reason := "题目标题或叙述触发了当前表现层的用语限制，题面检查未通过；这项结果不表示题意、算法或目标难度有误。"
	if match := statementForbiddenTermPattern.FindStringSubmatch(diagnostic); len(match) > 1 {
		reason = "题目标题或叙述使用了当前表现层检查禁止的词语「" + match[1] + "」，题面检查未通过；这项结果不表示题意、算法或目标难度有误。"
	}
	return &domain.GenerationFailure{Code: "statement_presentation_rejected", Category: "题面表达检查未通过", Stage: "编写题面", Message: "题面叙述与当前表达检查规则冲突", Reason: reason,
		Suggestion: "检查标题与叙述，调整表现层文案，或修正过严的表达检查后重试未完成题目；无需修改目标难度。", Retryable: &retryable}
}
