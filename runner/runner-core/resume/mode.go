package resume

import (
	"fmt"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
)

type Mode string

const (
	ModeResume      Mode = "resume"
	ModeRetryFailed Mode = "retry-failed"
)

func DefaultMode() Mode {
	return ModeResume
}

func (m Mode) Validate() error {
	switch m {
	case ModeResume, ModeRetryFailed:
		return nil
	default:
		return fmt.Errorf("resume mode must be one of %q, %q", ModeResume, ModeRetryFailed)
	}
}

func (m Mode) ShouldCarry(state domain.InstanceState) bool {
	switch m {
	case ModeResume:
		return state == domain.InstanceStateSucceeded || state == domain.InstanceStateTestFailed || state == domain.InstanceStateCanceled
	case ModeRetryFailed:
		return state == domain.InstanceStateSucceeded || state == domain.InstanceStateCanceled
	default:
		return false
	}
}
