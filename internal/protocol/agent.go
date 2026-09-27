package protocol

import (
	"errors"
	"regexp"
	"unicode/utf8"
)

const (
	MaxAgentOptions       = 6
	MaxAgentQuestionRunes = 160
	MaxAgentQuestionBytes = 480
	MaxAgentLabelRunes    = 80
	MaxAgentLabelBytes    = 240
)

var agentQuestionIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidAgentQuestionID reports whether value has the question-hash form.
func ValidAgentQuestionID(value string) bool { return agentQuestionIDPattern.MatchString(value) }

// validateAgentStatus applies the schema rules plus the one JSON Schema does
// not express here: question is present exactly while waiting.
func validateAgentStatus(status AgentStatus) error {
	if status.State != Working && status.State != Waiting && status.State != Idle {
		return errors.New("agent state is invalid")
	}
	if _, err := parseBrokerRPCTime(status.StateSince, "stateSince"); err != nil {
		return err
	}
	if (status.Question != nil) != (status.State == Waiting) {
		return errors.New("agent question is present only while waiting")
	}
	if status.Question == nil {
		return nil
	}
	question := status.Question
	if !ValidAgentQuestionID(question.ID) {
		return errors.New("agent question id is invalid")
	}
	if err := validateAgentText(question.Text, MaxAgentQuestionRunes, MaxAgentQuestionBytes, "question"); err != nil {
		return err
	}
	if len(question.Options) == 0 || len(question.Options) > MaxAgentOptions {
		return errors.New("agent options are outside bounds")
	}
	for _, option := range question.Options {
		if err := validateAgentText(option.Label, MaxAgentLabelRunes, MaxAgentLabelBytes, "option label"); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentText(value string, maxRunes, maxBytes int, name string) error {
	if err := validateBrokerRPCText(value, maxBytes, name); err != nil {
		return err
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return errors.New(name + " is outside bounds")
	}
	return nil
}
