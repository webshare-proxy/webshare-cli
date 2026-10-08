package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

// notificationSettings are the email subscriptions on the profile, in the
// order the account panel lists them.
var notificationSettings = []struct {
	name string
	get  func(*webshare.Profile) bool
	set  func(*webshare.ProfileUpdateParams, *bool)
}{
	{"Bandwidth usage", func(p *webshare.Profile) bool { return p.SubscribedBandwidthUsageNotifications },
		func(u *webshare.ProfileUpdateParams, v *bool) { u.SubscribedBandwidthUsageNotifications = v }},
	{"Subscription", func(p *webshare.Profile) bool { return p.SubscribedSubscriptionNotifications },
		func(u *webshare.ProfileUpdateParams, v *bool) { u.SubscribedSubscriptionNotifications = v }},
	{"Proxy usage statistics", func(p *webshare.Profile) bool { return p.SubscribedProxyUsageStatistics },
		func(u *webshare.ProfileUpdateParams, v *bool) { u.SubscribedProxyUsageStatistics = v }},
	{"Usage warnings", func(p *webshare.Profile) bool { return p.SubscribedUsageWarnings },
		func(u *webshare.ProfileUpdateParams, v *bool) { u.SubscribedUsageWarnings = v }},
	{"Guides and tips", func(p *webshare.Profile) bool { return p.SubscribedGuidesAndTips },
		func(u *webshare.ProfileUpdateParams, v *bool) { u.SubscribedGuidesAndTips = v }},
	{"Surveys", func(p *webshare.Profile) bool { return p.SubscribedSurveyEmails },
		func(u *webshare.ProfileUpdateParams, v *bool) { u.SubscribedSurveyEmails = v }},
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// setAccount stores the account and its active plans, keeping the sidebar on
// the same plan when it is still active. A failed load keeps the last good
// account, so the billing cycle and the plans stay known.
func (m *model) setAccount(msg accountLoaded) {
	previous := m.planID()
	m.accountErr = msg.err
	if msg.err != nil {
		return
	}
	m.account = &msg
	m.activePlans, m.plan = nil, 0
	for _, p := range msg.plans {
		if p.Status != webshare.PlanActive {
			continue
		}
		if p.ID == previous {
			m.plan = len(m.activePlans)
		}
		m.activePlans = append(m.activePlans, p)
	}
	rows := make([]table.Row, 0, len(notificationSettings))
	for _, setting := range notificationSettings {
		rows = append(rows, table.Row{setting.name, onOff(setting.get(msg.account.Profile))})
	}
	m.notifications.SetRows(rows)
	// The table the focus was on may be gone with the plans.
	if m.focus == 1 && m.focusable() == nil {
		m.focus = 0
	}
}

// accountStatus is the box shown while the account loads or after it failed;
// empty once it is loaded.
func (m model) accountStatus(width, height int) string {
	switch {
	case m.account == nil && m.accountErr != nil:
		return titledBox("Account", "error: "+m.accountErr.Error(), width, height, false)
	case m.account == nil:
		return titledBox("Account", dimStyle.Render("loading…"), width, height, false)
	}
	return ""
}

// accountView stacks the profile and its email notifications in their own
// boxes, filling width x height.
func (m model) accountView(width, height int) string {
	if status := m.accountStatus(width, height); status != "" {
		return status
	}
	profile := m.account.account.Profile
	name := strings.TrimSpace(profile.FirstName + " " + profile.LastName)
	profileBox := titledBox("Profile", keyValues([][2]string{
		{"Email", profile.Email},
		{"Name", name},
		{"Timezone", profile.Timezone},
		{"Last login", profile.LastLogin.Local().Format("2006-01-02 15:04")},
		{"Member since", profile.CreatedAt.Format("2006-01-02")},
	}), width, 0, m.focus == 0)
	rest := height - lipgloss.Height(profileBox)
	return lipgloss.JoinVertical(lipgloss.Left, profileBox,
		titledBox("Email notifications", m.notifications.View()+"\n"+m.bottomLine(), width, max(rest, 3), m.focus == 1))
}

// subscriptionView stacks the subscription and the history of every plan,
// cancelled ones included, filling width x height.
func (m model) subscriptionView(width, height int) string {
	if status := m.accountStatus(width, height); status != "" {
		return status
	}
	date := func(t time.Time) string { return t.Local().Format("2006-01-02") }
	s := m.account.account.Subscription
	subscriptionBox := titledBox("Subscription", keyValues([][2]string{
		{"Term", string(s.Term)},
		{"Started", date(s.StartDate)},
		{"Current period ends", date(s.EndDate)},
		{"Renewals paid", strconv.Itoa(s.RenewalsPaid)},
		{"Auto-renewal", strconv.FormatBool(s.RenewalsEnabled)},
		{"Will renew", strconv.FormatBool(s.WillRenew)},
		{"Failed payments", strconv.Itoa(s.FailedPaymentTimes)},
		{"Free credits", fmt.Sprintf("$%.2f", s.FreeCredits)},
		{"Account discount", fmt.Sprintf("%d%%", s.AccountDiscountPercentage)},
		{"Paused", strconv.FormatBool(s.Paused)},
		{"Throttled", strconv.FormatBool(s.Throttled)},
	}), width, 0, false)
	rows := make([][]string, 0, len(m.account.plans))
	for _, p := range m.account.plans {
		rows = append(rows, []string{
			strconv.Itoa(p.ID), string(p.Status), string(p.ProxyType), string(p.ProxySubtype),
			strconv.Itoa(p.ProxyCount), output.Bandwidth(p.BandwidthLimit),
			fmt.Sprintf("$%.2f", p.MonthlyPrice), fmt.Sprintf("$%.2f", p.YearlyPrice), date(p.CreatedAt),
		})
	}
	// ponytail: a static listing, it clips past the screen height; make it a
	// scrollable table if accounts with long plan histories show up.
	history := columns([]string{"ID", "STATUS", "TYPE", "SUBTYPE", "PROXIES", "BANDWIDTH", "MONTHLY", "YEARLY", "CREATED"}, rows)
	rest := height - lipgloss.Height(subscriptionBox)
	return lipgloss.JoinVertical(lipgloss.Left, subscriptionBox,
		titledBox("Plan history", history, width, max(rest, 3), false))
}

// accountAction handles the write keys of the account panel; ok is false
// when the key is not one of them.
func (m model) accountAction(key string) (next tea.Model, cmd tea.Cmd, ok bool) {
	if m.account == nil {
		return m, nil, false
	}
	profile := m.account.account.Profile
	switch {
	case key == "e":
		fields := [][2]string{
			{"first name: ", profile.FirstName},
			{"last name: ", profile.LastName},
			{"timezone (e.g. Europe/Madrid): ", profile.Timezone},
		}
		next, cmd = m.startForm(fields, func(m model, values []string) (model, tea.Cmd) {
			return m.updateProfile("profile updated", webshare.ProfileUpdateParams{
				FirstName: &values[0], LastName: &values[1], Timezone: &values[2],
			})
		})
		return next, cmd, true
	case m.focus == 1 && (key == " " || key == "enter"):
		cursor := m.notifications.Cursor()
		if cursor < 0 || cursor >= len(notificationSettings) {
			return m, nil, true
		}
		setting := notificationSettings[cursor]
		value := !setting.get(profile)
		var params webshare.ProfileUpdateParams
		setting.set(&params, &value)
		next, cmd = m.updateProfile(fmt.Sprintf("%s emails %s", setting.name, onOff(value)), params)
		return next, cmd, true
	}
	return m, nil, false
}

func (m model) updateProfile(notice string, params webshare.ProfileUpdateParams) (model, tea.Cmd) {
	m.notices[accountTarget] = "working…"
	return m, func() tea.Msg {
		_, err := m.client.Profile.Update(m.ctx, params)
		return actionDone{target: accountTarget, notice: notice, err: err}
	}
}
