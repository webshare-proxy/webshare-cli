package tui

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

// activityLimit caps the activity tab like `webshare activity list` does.
const activityLimit = 100

// Time ranges of the stats tabs, in the order t cycles through them.
const (
	range24h = iota
	range7d
	rangeCycle
	rangeCount
)

// errNoBillingCycle is returned for the billing cycle range before the
// subscription, which holds its dates, has loaded.
var errNoBillingCycle = errors.New("the billing cycle is not known until the subscription loads")

// rangeBounds is the window a range covers; the billing cycle is the
// subscription's current period, as the dashboard's This Billing Cycle.
func (m model) rangeBounds(r int) (from, until *time.Time, err error) {
	switch r {
	case rangeCycle:
		if m.account == nil {
			return nil, nil, errNoBillingCycle
		}
		s := m.account.account.Subscription
		return &s.StartDate, &s.EndDate, nil
	case range7d:
		from := time.Now().Add(-7 * 24 * time.Hour)
		return &from, nil, nil
	}
	from = new(time.Time)
	*from = time.Now().Add(-24 * time.Hour)
	return from, nil, nil
}

func (m model) rangeLabel(r int) string {
	switch r {
	case range7d:
		return "last 7 days"
	case rangeCycle:
		from, until, err := m.rangeBounds(r)
		if err != nil {
			return "this billing cycle"
		}
		return fmt.Sprintf("this billing cycle (%s to %s)", from.Local().Format("2006-01-02"), until.Local().Format("2006-01-02"))
	}
	return "last 24 hours"
}

// rangeSince is the --since value of a range, empty when the CLI cannot
// express it (the billing cycle).
func rangeSince(r int) string {
	switch r {
	case range24h:
		return "24h"
	case range7d:
		return "7d"
	}
	return ""
}

func (m model) fetchAggregate(planID, r int) (any, bool, error) {
	from, until, err := m.rangeBounds(r)
	if err != nil {
		return nil, false, err
	}
	stats, err := m.client.Stats.Aggregate(m.ctx, webshare.StatsListParams{
		TimestampGTE: from, TimestampLTE: until, PlanID: webshare.Int(planID),
	})
	return stats, false, err
}

func (m model) fetchActivity(planID, r int) (any, bool, error) {
	from, until, err := m.rangeBounds(r)
	if err != nil {
		return nil, false, err
	}
	params := webshare.ProxyActivityListParams{
		TimestampGTE: from, TimestampLTE: until, PlanID: webshare.Int(planID),
		Search: m.activitySearch, ErrorReason: m.activityError,
	}
	var activities []webshare.ProxyActivity
	for activity, err := range m.client.ProxyActivity.ListAll(m.ctx, params) {
		if err != nil {
			return nil, false, err
		}
		if len(activities) == activityLimit {
			return activities, true, nil
		}
		activities = append(activities, activity)
	}
	return activities, false, nil
}

// bandwidthView is the usage bar against the plan's limit, then the totals.
func (m model) bandwidthView(width int) string {
	stats := m.shown[bandwidthTab].data.(*webshare.AggregateStats)
	plan := m.activePlans[m.plan]
	var b strings.Builder
	b.WriteString(dimStyle.Render(m.rangeLabel(m.ranges[bandwidthTab])) + "\n\n")
	if plan.BandwidthLimit > 0 {
		// The plan limit is in GB and the stats in bytes. The backend counts a
		// GB as 1024³ bytes (proxycontrolpanel's Plan.get_limit_in_bytes).
		limit := plan.BandwidthLimit * (1 << 30)
		b.WriteString(usageBar(float64(stats.BandwidthTotal), float64(stats.BandwidthProjected), limit, max(width-24, 10)))
		b.WriteString(fmt.Sprintf("  %.1f%% of %s\n\n", 100*float64(stats.BandwidthTotal)/limit, output.Bandwidth(plan.BandwidthLimit)))
		b.WriteString(keyValues([][2]string{
			{"█ Used", output.Bytes(stats.BandwidthTotal)},
			{"▒ Projected", output.Bytes(stats.BandwidthProjected)},
			{"░ Remaining", output.Bytes(int64(max(limit-float64(stats.BandwidthProjected), 0)))},
		}))
	} else {
		b.WriteString(keyValues([][2]string{
			{"Used", output.Bytes(stats.BandwidthTotal) + " (unlimited plan)"},
			{"Projected", output.Bytes(stats.BandwidthProjected)},
		}))
	}
	lastRequest := "never"
	if stats.LastRequestSentAt != nil {
		lastRequest = stats.LastRequestSentAt.Local().Format("2006-01-02 15:04")
	}
	optional := func(v *float64) string {
		if v == nil {
			return "-"
		}
		return strconv.FormatFloat(*v, 'f', 2, 64)
	}
	b.WriteString("\n" + keyValues([][2]string{
		{"Requests", strconv.FormatInt(stats.RequestsTotal, 10)},
		{"Unique proxies used", strconv.Itoa(stats.NumberOfProxiesUsed)},
		{"Average concurrency", optional(stats.AverageConcurrency)},
		{"Average requests/s", optional(stats.AverageRPS)},
		{"Last request", lastRequest},
		{"Protocols", breakdown(stats.ProtocolsUsed)},
		{"Countries", breakdown(stats.CountriesUsed)},
		{"High priority network", onOff(plan.IsHighPriorityNetwork)},
		{"High concurrency", onOff(plan.IsHighConcurrency)},
	}))
	return b.String()
}

// usageBar draws used (█) and projected (▒) over the limit (░), in red when
// the projection goes over the limit.
func usageBar(used, projected, limit float64, width int) string {
	cells := func(v float64) int { return min(int(math.Round(v/limit*float64(width))), width) }
	usedCells := cells(used)
	projectedCells := max(cells(projected)-usedCells, 0)
	bar := strings.Repeat("█", usedCells) + strings.Repeat("▒", projectedCells) +
		strings.Repeat("░", width-usedCells-projectedCells)
	if projected > limit {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(bar)
	}
	return keyStyle.Render(bar)
}

// breakdown lists counts by key, largest first.
func breakdown(counts map[string]int64) string {
	if len(counts) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, counts[k])
	}
	return strings.Join(parts, " · ")
}

func (m *model) setErrors(msg tabLoaded) {
	stats := msg.data.(*webshare.AggregateStats)
	rows := make([]table.Row, 0, len(stats.ErrorReasons))
	for _, reason := range stats.ErrorReasons {
		rate, status := "-", "-"
		if stats.RequestsTotal > 0 {
			rate = fmt.Sprintf("%.2f%%", 100*float64(reason.Count)/float64(stats.RequestsTotal))
		}
		if reason.HTTPStatus != nil {
			status = strconv.Itoa(*reason.HTTPStatus)
		}
		rows = append(rows, table.Row{rate, strconv.Itoa(reason.Count), reason.Type, reason.Reason, status})
	}
	m.errorReasons.SetRows(rows)
}

// errorsSummary is the totals line above the error reasons.
func (m model) errorsSummary() string {
	stats := m.shown[errorsTab].data.(*webshare.AggregateStats)
	rate := "-"
	if stats.RequestsTotal > 0 {
		rate = fmt.Sprintf("%.2f%%", 100*float64(stats.RequestsFailed)/float64(stats.RequestsTotal))
	}
	return dimStyle.Render(m.rangeLabel(m.ranges[errorsTab])) + "\n" + keyValues([][2]string{
		{"Total requests", strconv.FormatInt(stats.RequestsTotal, 10)},
		{"Total errors", strconv.FormatInt(stats.RequestsFailed, 10)},
		{"Error rate", rate},
	})
}

func (m *model) setActivity(msg tabLoaded) {
	activities := msg.data.([]webshare.ProxyActivity)
	rows := make([]table.Row, 0, len(activities))
	for _, a := range activities {
		target := ""
		switch {
		case a.Domain != nil:
			target = *a.Domain
		case a.Hostname != nil:
			target = *a.Hostname
		case a.IPAddress != nil:
			target = *a.IPAddress
		}
		port, proxy, result := "", "", "ok"
		if a.Port != nil {
			port = strconv.Itoa(*a.Port)
		}
		if a.ProxyAddress != nil {
			proxy = *a.ProxyAddress
		}
		if a.ErrorReason != nil {
			result = *a.ErrorReason
		}
		rows = append(rows, table.Row{
			a.Timestamp.Local().Format("01-02 15:04:05"), target, port, output.Bytes(int64(a.Bytes)),
			fmt.Sprintf("%.2fs", a.RequestDuration), proxy, a.ClientAddress, result, a.Protocol,
		})
	}
	m.activity.SetRows(rows)
}

// activityStatus is the line under the activity table.
func (m model) activityStatus() string {
	msg := m.shown[activityTab]
	count := len(msg.data.([]webshare.ProxyActivity))
	status := fmt.Sprintf("%d requests", count)
	if msg.truncated {
		status = fmt.Sprintf("latest %d requests", count)
	}
	status += " · " + m.rangeLabel(m.ranges[activityTab])
	if m.activitySearch != "" {
		status += " · search " + m.activitySearch
	}
	if m.activityError != "" {
		status += " · error " + m.activityError
	}
	return status
}

// activityCommand is the CLI command that lists what the activity tab shows,
// empty for the billing cycle, which --since cannot express.
func (m model) activityCommand() string {
	since := rangeSince(m.ranges[activityTab])
	if since == "" {
		return ""
	}
	command := "webshare activity list --since " + since
	if m.activitySearch != "" {
		command += " --search " + m.activitySearch
	}
	if m.activityError != "" {
		command += " --error '" + m.activityError + "'"
	}
	return command
}
