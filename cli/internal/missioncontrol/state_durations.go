package missioncontrol

import (
	"sort"
	"strconv"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

func deriveSimplifiedStateDurations(inst *runnerapi.InstanceSnapshot, now time.Time) map[simplifiedState]time.Duration {
	if inst == nil || len(inst.Events) == 0 {
		return map[simplifiedState]time.Duration{}
	}

	now = now.UTC()
	events := append([]store.InstanceEvent(nil), inst.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})

	durations := make(map[simplifiedState]time.Duration, len(baseVisibleSimplifiedStates)+1)
	for idx, event := range events {
		start := event.CreatedAt.UTC()
		if start.IsZero() {
			continue
		}

		end := now
		if idx+1 < len(events) {
			end = events[idx+1].CreatedAt.UTC()
		}
		if !end.After(start) {
			continue
		}

		state := simplifiedStateForInstanceState(event.ToState)
		if !showsDurationForSimplifiedState(state) {
			continue
		}
		durations[state] += end.Sub(start)
	}

	return durations
}

func showsDurationForSimplifiedState(state simplifiedState) bool {
	if state == simplifiedStatePending {
		return false
	}
	return !simplifiedStateIsTerminal(state)
}

func formatSimplifiedStateDuration(d time.Duration) string {
	if d < time.Second {
		return ""
	}

	d = d.Truncate(time.Second)
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second

	switch {
	case hours > 0:
		if minutes > 0 {
			return formatHoursMinutes(hours, minutes)
		}
		return formatHoursOnly(hours)
	case minutes > 0:
		if seconds > 0 {
			return formatMinutesSeconds(minutes, seconds)
		}
		return formatMinutesOnly(minutes)
	default:
		return formatSecondsOnly(seconds)
	}
}

func formatHoursMinutes(hours, minutes time.Duration) string {
	return pluralDuration(hours, "h") + zeroPaddedDuration(minutes, "m")
}

func formatHoursOnly(hours time.Duration) string {
	return pluralDuration(hours, "h")
}

func formatMinutesSeconds(minutes, seconds time.Duration) string {
	return pluralDuration(minutes, "m") + zeroPaddedDuration(seconds, "s")
}

func formatMinutesOnly(minutes time.Duration) string {
	return pluralDuration(minutes, "m")
}

func formatSecondsOnly(seconds time.Duration) string {
	return pluralDuration(seconds, "s")
}

func pluralDuration(value time.Duration, suffix string) string {
	return strconvFormatInt(int64(value)) + suffix
}

func zeroPaddedDuration(value time.Duration, suffix string) string {
	if value < 10 {
		return "0" + strconvFormatInt(int64(value)) + suffix
	}
	return strconvFormatInt(int64(value)) + suffix
}

func strconvFormatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
