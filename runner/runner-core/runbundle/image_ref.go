package runbundle

import (
	"regexp"
	"strings"
)

var pinnedImagePattern = regexp.MustCompile(`^(?:[^\s@]+@sha256:[a-f0-9]{64}|sha256:[a-f0-9]{64})$`)

func IsPinnedImageRef(image string) bool {
	return pinnedImagePattern.MatchString(strings.TrimSpace(image))
}
