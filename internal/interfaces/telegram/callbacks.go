package telegram

import "strings"

type CallbackKind string

const (
	CallbackUnknown          CallbackKind = "unknown"
	CallbackCustomWindow    CallbackKind = "custom_window"
	CallbackCustomGran      CallbackKind = "custom_granularity"
	CallbackCalendarIgnore  CallbackKind = "calendar_ignore"
	CallbackCalendar        CallbackKind = "calendar"
	CallbackRangeFrom       CallbackKind = "range_from"
	CallbackRangeTo         CallbackKind = "range_to"
	CallbackRangeGran       CallbackKind = "range_granularity"
	CallbackRangeHistory    CallbackKind = "range_history"
	CallbackDateRangeGran   CallbackKind = "date_range_granularity"
)

type CallbackRoute struct {
	Kind  CallbackKind
	Parts []string
	Raw   string
}

func ParseCallbackData(data string) CallbackRoute {
	route := CallbackRoute{Kind: CallbackUnknown, Raw: data}
	s := strings.TrimSpace(data)
	switch {
	case strings.HasPrefix(s, "ccpw_"):
		route.Kind = CallbackCustomWindow
		route.Parts = []string{strings.TrimPrefix(s, "ccpw_")}
	case strings.HasPrefix(s, "ccpg_"):
		route.Kind = CallbackCustomGran
		route.Parts = strings.Split(strings.TrimPrefix(s, "ccpg_"), "_")
	case s == "ccal_ignore":
		route.Kind = CallbackCalendarIgnore
	case strings.HasPrefix(s, "ccal_"):
		route.Kind = CallbackCalendar
		route.Parts = strings.SplitN(strings.TrimPrefix(s, "ccal_"), "_", 3)
	case strings.HasPrefix(s, "cprf_"):
		route.Kind = CallbackRangeFrom
		route.Parts = []string{strings.TrimPrefix(s, "cprf_")}
	case strings.HasPrefix(s, "cprt_"):
		route.Kind = CallbackRangeTo
		route.Parts = []string{strings.TrimPrefix(s, "cprt_")}
	case strings.HasPrefix(s, "cprg_"):
		route.Kind = CallbackRangeGran
		route.Parts = strings.Split(strings.TrimPrefix(s, "cprg_"), "_")
	case strings.HasPrefix(s, "cprh_"):
		route.Kind = CallbackRangeHistory
		route.Parts = strings.Split(strings.TrimPrefix(s, "cprh_"), "_")
	case strings.HasPrefix(s, "cpdtg_"):
		route.Kind = CallbackDateRangeGran
		route.Parts = strings.Split(strings.TrimPrefix(s, "cpdtg_"), "_")
	}
	return route
}
