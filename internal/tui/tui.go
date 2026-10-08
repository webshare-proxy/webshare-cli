// Package tui is the interactive front-end launched by `webshare ui`. It reads
// through internal/app and the SDK, never through the cobra commands.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/webshare-proxy/webshare-cli/internal/app"
	webshare "github.com/webshare-proxy/webshare-go"
)

// proxyLimit caps the proxies tab like `webshare proxies list` does.
const proxyLimit = 100

// sidebarWidth fits " Rotating Residential", the longest product name.
const sidebarWidth = 26

// columnGap is the space between columns in every panel.
const columnGap = 4

// margin starts every line of text in a panel, aligned with the tables,
// whose cells are padded by columnGap/2.
const margin = "  "

// The tabs of a plan, on the right of the plans sidebar.
var tabs = []string{"Proxies", "Bandwidth", "Errors", "Activity", "Authorized IPs", "Sub-users"}

const (
	proxiesTab = iota
	bandwidthTab
	errorsTab
	activityTab
	ipsTab
	subusersTab
)

// accountTarget keys the account screen in notices and actionDone, next to
// the tab indexes.
const accountTarget = -1

// Screens: the plans view, and the full-screen account and subscription.
const (
	plansScreen = iota
	accountScreen
	subscriptionScreen
)

// Run draws the UI until the user quits or ctx is cancelled.
func Run(ctx context.Context, client *webshare.Client) error {
	_, err := tea.NewProgram(newModel(ctx, client), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) {
		return ctx.Err()
	}
	return err
}

type accountLoaded struct {
	account *app.Account
	// plans holds active and cancelled plans.
	plans []webshare.Plan
	err   error
}

// tabLoaded is one tab's answer for one plan. It carries the plan so a late
// answer for a plan that is no longer selected is cached but not shown.
type tabLoaded struct {
	planID int
	tab    int
	// key is where the answer is cached (cacheKey): it holds what the answer
	// depends on besides the plan, so an answer for another mode, filter or
	// range is not reused.
	key string
	// requestedAt is when the request started: it orders answers, so a late
	// older one never replaces a newer one, and it starts the cacheTTL.
	requestedAt time.Time
	// data is []webshare.Proxy, *webshare.AggregateStats,
	// []webshare.ProxyActivity, []webshare.IPAuthorization or
	// []webshare.Subuser, depending on the tab.
	data      any
	truncated bool
	err       error
}

// planSettled fires once the sidebar has rested on a plan for
// planDebounce, so scrolling past plans does not fetch each one. move is the
// sidebar move it was scheduled for; a later move cancels it.
type planSettled struct{ move int }

const planDebounce = 250 * time.Millisecond

// cacheTTL is how long a cached answer is shown without fetching it again.
const cacheTTL = 5 * time.Minute

// actionDone reports a write; target is a tab of planID, or accountTarget
// (planID 0).
type actionDone struct {
	planID int
	target int
	notice string
	err    error
}

// form asks for its fields one at a time in the panel's bottom line.
type form struct {
	fields [][2]string // prompt, current value
	step   int
	submit func(m model, values []string) (model, tea.Cmd)
}

// confirmation is a pending destructive action waiting for y.
type confirmation struct {
	question string
	run      tea.Cmd
}

type model struct {
	ctx    context.Context
	client *webshare.Client

	screen int
	// plan is the sidebar cursor over the active plans.
	plan int
	// moves counts sidebar moves, to drop a planSettled overtaken by one.
	moves int
	// frame advances the loading shimmer; ticking is set while a
	// shimmerTick is scheduled, so only one runs at a time.
	frame   int
	ticking bool
	tab     int
	// focus is 0 on the sidebar (or the screen), 1 on the table.
	focus  int
	width  int
	height int

	// account is the last account that loaded, nil until one does; a failed
	// reload keeps it and sets accountErr.
	account       *accountLoaded
	accountErr    error
	activePlans   []webshare.Plan
	notifications table.Model

	// shown is the selected plan's answer for each tab; a missing tab is
	// loading.
	shown map[int]*tabLoaded
	// cache holds the last successful answers, keyed by plan ID then
	// cacheKey. An answer older than cacheTTL is fetched again the next time
	// its tab is shown. Refresh (r) drops the plan's entry.
	cache map[int]map[string]tabLoaded
	// inflight marks the requests started and not answered yet, by plan and
	// cache key, so revisiting a tab does not send its request twice.
	inflight map[string]bool

	proxies   table.Model
	proxyList []webshare.Proxy
	mode      string
	countries []string

	errorReasons table.Model

	activity       table.Model
	activitySearch string
	activityError  string

	// ranges is the time range of the bandwidth, errors and activity tabs.
	ranges map[int]int
	// hscroll is how many cells each tab's table is scrolled to the right.
	hscroll map[int]int

	ips         table.Model
	subusers    table.Model
	auths       []webshare.IPAuthorization
	subuserList []webshare.Subuser

	input    textinput.Model
	form     *form
	confirm  *confirmation
	showHelp bool
	// notices is the last action's feedback line, keyed by tab or
	// accountTarget.
	notices map[int]string
}

func newTable(columns ...table.Column) table.Model {
	t := table.New(table.WithColumns(columns))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Padding(0, columnGap/2)
	styles.Cell = styles.Cell.Padding(0, columnGap/2)
	t.SetStyles(styles)
	return t
}

func newModel(ctx context.Context, client *webshare.Client) model {
	return model{
		ctx: ctx, client: client, mode: "direct",
		input:    textinput.New(),
		notices:  map[int]string{},
		shown:    map[int]*tabLoaded{},
		cache:    map[int]map[string]tabLoaded{},
		inflight: map[string]bool{},
		ranges:   map[int]int{bandwidthTab: rangeCycle, errorsTab: rangeCycle, activityTab: range24h},
		hscroll:  map[int]int{},
		notifications: newTable(
			table.Column{Title: "NOTIFICATION", Width: 24},
			table.Column{Title: "EMAIL", Width: 5},
		),
		proxies: newTable(
			table.Column{Title: "ADDRESS", Width: 18},
			table.Column{Title: "PORT", Width: 6},
			table.Column{Title: "COUNTRY", Width: 8},
			table.Column{Title: "CITY", Width: 16},
			table.Column{Title: "VALID", Width: 5},
		),
		errorReasons: newTable(
			table.Column{Title: "RATE", Width: 7},
			table.Column{Title: "COUNT", Width: 6},
			table.Column{Title: "TYPE", Width: 14},
			table.Column{Title: "REASON", Width: 22},
			table.Column{Title: "HTTP", Width: 4},
		),
		activity: newTable(
			table.Column{Title: "TIME", Width: 14},
			table.Column{Title: "DOMAIN", Width: 22},
			table.Column{Title: "PORT", Width: 5},
			table.Column{Title: "BYTES", Width: 9},
			table.Column{Title: "DURATION", Width: 8},
			table.Column{Title: "PROXY", Width: 15},
			table.Column{Title: "YOUR IP", Width: 15},
			table.Column{Title: "ERROR", Width: 16},
			table.Column{Title: "PROTOCOL", Width: 8},
		),
		ips: newTable(
			table.Column{Title: "IP", Width: 18},
			table.Column{Title: "ADDED", Width: 10},
			table.Column{Title: "LAST USED", Width: 16},
		),
		subusers: newTable(
			table.Column{Title: "LABEL", Width: 20},
			table.Column{Title: "BANDWIDTH LIMIT", Width: 15},
			table.Column{Title: "MAX THREADS", Width: 11},
			table.Column{Title: "USED", Width: 10},
		),
	}
}

// focusable is the table that tab focuses, or nil when there is none.
func (m *model) focusable() *table.Model {
	switch {
	case m.screen == accountScreen:
		return &m.notifications
	case m.screen != plansScreen || len(m.activePlans) == 0:
		return nil
	case m.tab == proxiesTab:
		return &m.proxies
	case m.tab == errorsTab:
		return &m.errorReasons
	case m.tab == activityTab:
		return &m.activity
	case m.tab == ipsTab:
		return &m.ips
	case m.tab == subusersTab:
		return &m.subusers
	}
	return nil
}

// planID is the selected plan, 0 before the plans load.
func (m model) planID() int {
	if m.plan < len(m.activePlans) {
		return m.activePlans[m.plan].ID
	}
	return 0
}

// Init names the terminal window and loads the account. Sequence, not
// Batch: bubbletea v1.3.7 would run a batched request on its event loop (see
// Update).
func (m model) Init() tea.Cmd {
	return tea.Sequence(tea.SetWindowTitle("webshare"), m.loadAccount)
}

func (m model) loadAccount() tea.Msg {
	account, err := app.GetAccount(m.ctx, m.client)
	if err != nil {
		return accountLoaded{err: err}
	}
	plans, err := app.ListPlans(m.ctx, m.client, true)
	return accountLoaded{account: account, plans: plans, err: err}
}

// showPlan shows the selected plan from the cache and, after planDebounce,
// fetches whatever the cache is missing.
func (m model) showPlan() (model, tea.Cmd) {
	m = m.applyCache()
	// A notice belongs to the plan it was about.
	m.notices = map[int]string{accountTarget: m.notices[accountTarget]}
	m.moves++
	if _, ok := m.fresh(m.tab); m.planID() == 0 || ok {
		return m, nil
	}
	move := m.moves
	return m, tea.Tick(planDebounce, func(time.Time) tea.Msg { return planSettled{move: move} })
}

// applyCache resets every tab and fills the ones the selected plan has a
// fresh answer for.
func (m model) applyCache() model {
	m.shown = map[int]*tabLoaded{}
	for _, t := range []*table.Model{&m.proxies, &m.errorReasons, &m.activity, &m.ips, &m.subusers} {
		t.SetRows(nil)
	}
	m.proxyList, m.auths, m.subuserList = nil, nil, nil
	for tab := range tabs {
		if msg, ok := m.fresh(tab); ok {
			m.apply(msg)
		}
	}
	return m
}

// loadMissing fetches the visible tab when the selected plan has no fresh
// answer for it and none is on its way. The other tabs load when opened.
func (m model) loadMissing() tea.Cmd {
	if _, ok := m.fresh(m.tab); m.planID() == 0 || ok || m.inflight[m.inflightKey(m.tab)] {
		return nil
	}
	m.inflight[m.inflightKey(m.tab)] = true
	return m.load(m.tab)
}

// fresh is the selected plan's cached answer for tab, unless it expired.
func (m model) fresh(tab int) (tabLoaded, bool) {
	msg, ok := m.cache[m.planID()][m.cacheKey(tab)]
	if !ok || time.Since(msg.requestedAt) >= cacheTTL {
		return tabLoaded{}, false
	}
	msg.tab = tab
	return msg, true
}

// cacheKey is where a tab's answer is cached. Bandwidth and Errors share the
// aggregate stats, so on the same range they share one request and answer.
func (m model) cacheKey(tab int) string {
	name := tabs[tab]
	if tab == bandwidthTab || tab == errorsTab {
		name = "aggregate"
	}
	return name + "|" + m.query(tab)
}

func (m model) inflightKey(tab int) string {
	return strconv.Itoa(m.planID()) + "|" + m.cacheKey(tab)
}

// query is what a tab's answer depends on besides the plan.
func (m model) query(tab int) string {
	switch tab {
	case proxiesTab:
		return m.proxiesCommand()
	case bandwidthTab, errorsTab:
		return m.rangeLabel(m.ranges[tab])
	case activityTab:
		return m.rangeLabel(m.ranges[tab]) + "|" + m.activitySearch + "|" + m.activityError
	}
	return ""
}

// load fetches one tab of the selected plan.
func (m model) load(tab int) tea.Cmd {
	// Everything the fetch reads is taken here: it runs on another goroutine
	// while Update keeps changing the model's maps.
	planID, r := m.planID(), m.ranges[tab]
	msg := tabLoaded{planID: planID, tab: tab, key: m.cacheKey(tab), requestedAt: time.Now()}
	return func() tea.Msg {
		msg.data, msg.truncated, msg.err = m.fetch(tab, planID, r)
		return msg
	}
}

// fetch reads one tab; r is the tab's time range, if it has one.
func (m model) fetch(tab, planID, r int) (any, bool, error) {
	switch tab {
	case proxiesTab:
		params := webshare.ProxyListParams{Mode: webshare.ConnectionMode(m.mode), CountryCodeIn: m.countries, PlanID: webshare.Int(planID)}
		return app.ListProxies(m.ctx, m.client, params, proxyLimit)
	case bandwidthTab, errorsTab:
		return m.fetchAggregate(planID, r)
	case activityTab:
		return m.fetchActivity(planID, r)
	case ipsTab:
		var auths []webshare.IPAuthorization
		for auth, err := range m.client.IPAuthorizations.ListAll(m.ctx, webshare.IPAuthorizationListParams{PlanID: webshare.Int(planID)}) {
			if err != nil {
				return nil, false, err
			}
			auths = append(auths, auth)
		}
		return auths, false, nil
	}
	var subusers []webshare.Subuser
	for subuser, err := range m.client.Subusers.ListAll(m.ctx, webshare.SubuserListParams{PlanID: webshare.Int(planID)}) {
		if err != nil {
			return nil, false, err
		}
		subusers = append(subusers, subuser)
	}
	return subusers, false, nil
}

// apply shows an answer of the selected plan.
func (m *model) apply(msg tabLoaded) {
	m.shown[msg.tab] = &msg
	if msg.err != nil {
		return
	}
	switch msg.tab {
	case proxiesTab:
		m.setProxies(msg)
	case errorsTab:
		m.setErrors(msg)
	case activityTab:
		m.setActivity(msg)
	case ipsTab:
		m.setIPs(msg)
	case subusersTab:
		m.setSubusers(msg)
	}
}

// reloadTab refetches one tab of the selected plan and shows it loading:
// after a mode, range or filter change what it showed answers another query.
func (m model) reloadTab(tab int) (model, tea.Cmd) {
	delete(m.shown, tab)
	return m, m.load(tab)
}

// shimmerTick advances the loading shimmer.
type shimmerTick struct{}

const shimmerInterval = 90 * time.Millisecond

// Update runs update and keeps the shimmer ticking while something shows as
// loading; with nothing loading no tick is scheduled.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(shimmerTick); ok {
		m.frame++
		m.ticking = false
	}
	next, cmd := m.update(msg)
	m = next.(model)
	if m.loading() && !m.ticking {
		m.ticking = true
		tick := tea.Tick(shimmerInterval, func(time.Time) tea.Msg { return shimmerTick{} })
		if cmd == nil {
			return m, tick
		}
		// ponytail: bubbletea v1.3.7 runs a batch's commands inside its event
		// loop (`go p.Send(cmd())` calls cmd() first), so a request batched
		// with the tick would freeze the UI until it answers. A sequence runs
		// the batch off the loop; the no-op step only keeps Sequence from
		// unwrapping a lone command. Use a plain tea.Batch from bubbletea
		// v1.3.9, which needs Go 1.24.
		cmd = tea.Sequence(tea.Batch(cmd, tick), func() tea.Msg { return nil })
	}
	return m, cmd
}

// loading reports whether the screen shows the loading shimmer.
func (m model) loading() bool {
	if m.account == nil && m.accountErr == nil {
		return true
	}
	return m.screen == plansScreen && m.planID() != 0 && m.shown[m.tab] == nil
}

// shimmerIcons spin in front of the word: a half-filled circle turning,
// close to the dashboard's loader.
var shimmerIcons = []string{"◐", "◓", "◑", "◒"}

// shimmer is the loading indicator: a spinning circle, then "loading..." dimmed
// with a light sweeping across it a letter per frame and resting off the
// word between sweeps.
func shimmer(frame int) string {
	const word = "loading..."
	position := frame%(len(word)+6) - 1
	var b strings.Builder
	b.WriteString(keyStyle.Render(shimmerIcons[frame%len(shimmerIcons)]) + " ")
	for i, letter := range word {
		switch i - position {
		case 0:
			b.WriteString(keyStyle.Bold(true).Render(string(letter)))
		case -1, 1:
			b.WriteString(string(letter))
		default:
			b.WriteString(dimStyle.Render(string(letter)))
		}
	}
	return b.String()
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Tables get their full width, never cut: the panel clips them at its
		// border and ←/→ scrolls what does not fit (tableView).
		for _, t := range []*table.Model{&m.notifications, &m.proxies, &m.errorReasons, &m.activity, &m.ips, &m.subusers} {
			t.SetWidth(tableWidth(*t))
		}
		m.notifications.SetHeight(len(notificationSettings))
		// Each table leaves room for its header and the lines around it.
		m.proxies.SetHeight(max(m.height-7, 1))
		m.activity.SetHeight(max(m.height-7, 1))
		m.errorReasons.SetHeight(max(m.height-11, 1))
		m.ips.SetHeight(max(m.height-6, 1))
		m.subusers.SetHeight(max(m.height-6, 1))
		return m, nil
	case accountLoaded:
		previous := m.planID()
		m.setAccount(msg)
		if m.planID() != previous {
			m = m.applyCache()
		}
		// A new billing cycle changes the stats tabs' query, so they reload.
		return m, m.loadMissing()
	case planSettled:
		if msg.move == m.moves {
			return m, m.loadMissing()
		}
		return m, nil
	// Answers are cached even when their plan is no longer selected. Only the
	// selected plan's are shown, on every tab whose cache key they answer
	// (Bandwidth and Errors share one). An answer older than the one already
	// cached or shown is dropped.
	case tabLoaded:
		delete(m.inflight, strconv.Itoa(msg.planID)+"|"+msg.key)
		cached, ok := m.cache[msg.planID][msg.key]
		if msg.err == nil && !(ok && msg.requestedAt.Before(cached.requestedAt)) {
			if m.cache[msg.planID] == nil {
				m.cache[msg.planID] = map[string]tabLoaded{}
			}
			m.cache[msg.planID][msg.key] = msg
		}
		if msg.planID != m.planID() {
			return m, nil
		}
		for tab := range tabs {
			shown := m.shown[tab]
			if m.cacheKey(tab) != msg.key || (shown != nil && msg.requestedAt.Before(shown.requestedAt)) {
				continue
			}
			msg.tab = tab
			m.apply(msg)
		}
		return m, nil
	case actionDone:
		if msg.target == accountTarget {
			if msg.err != nil {
				m.notices[accountTarget] = "error: " + msg.err.Error()
				return m, nil
			}
			m.notices[accountTarget] = msg.notice
			return m, m.loadAccount
		}
		// The write's plan may no longer be selected: its cached tab goes
		// either way, and only a selected plan shows the notice and reloads.
		if msg.err == nil {
			for key, cached := range m.cache[msg.planID] {
				if cached.tab == msg.target {
					delete(m.cache[msg.planID], key)
				}
			}
		}
		if msg.planID != m.planID() {
			return m, nil
		}
		if msg.err != nil {
			m.notices[msg.target] = "error: " + msg.err.Error()
			return m, nil
		}
		m.notices[msg.target] = msg.notice
		return m, m.load(msg.target)
	case clipboardDone:
		m.notices[proxiesTab] = copyFeedback(msg)
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// noticeTarget is where the current screen's feedback line is kept.
func (m model) noticeTarget() int {
	if m.screen == plansScreen {
		return m.tab
	}
	return accountTarget
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch {
	case m.form != nil:
		return m.updateForm(msg)
	case m.confirm != nil:
		run := m.confirm.run
		m.confirm = nil
		if key == "y" {
			m.notices[m.noticeTarget()] = "working…"
			return m, run
		}
		m.notices[m.noticeTarget()] = "cancelled"
		return m, nil
	case m.showHelp:
		if key == "?" || key == "esc" || key == "q" {
			m.showHelp = false
		}
		return m, nil
	}
	switch key {
	case "?":
		m.showHelp = true
		return m, nil
	case "q":
		return m, tea.Quit
	case "r":
		return m.reload()
	case "tab":
		if t := m.focusable(); t != nil {
			m.focus = 1 - m.focus
			if m.focus == 1 {
				t.Focus()
			} else {
				t.Blur()
			}
		}
		return m, nil
	case "a", "s":
		screen := accountScreen
		if key == "s" {
			screen = subscriptionScreen
		}
		if m.screen == screen {
			screen = plansScreen
		}
		return m.switchScreen(screen), nil
	case "esc":
		if m.screen != plansScreen {
			return m.switchScreen(plansScreen), nil
		}
	}
	switch m.screen {
	case accountScreen:
		if next, cmd, ok := m.accountAction(key); ok {
			return next, cmd
		}
	case plansScreen:
		if next, cmd, ok := m.plansAction(msg); ok {
			return next, cmd
		}
	}
	if t := m.focusable(); m.focus == 1 && t != nil {
		var cmd tea.Cmd
		*t, cmd = t.Update(msg)
		return m, cmd
	}
	m.focus = 0
	if m.screen == plansScreen && len(m.activePlans) > 0 {
		previous := m.plan
		switch key {
		case "j", "down":
			m.plan = (m.plan + 1) % len(m.activePlans)
		case "k", "up":
			m.plan = (m.plan + len(m.activePlans) - 1) % len(m.activePlans)
		}
		if m.plan != previous {
			return m.showPlan()
		}
	}
	return m, nil
}

// switchScreen leaves the focus on the new screen's sidebar.
func (m model) switchScreen(screen int) model {
	if t := m.focusable(); t != nil {
		t.Blur()
	}
	m.screen, m.focus = screen, 0
	return m
}

// plansAction handles the keys of the plans screen that are not plain
// movement; ok is false when the key is not one of them.
func (m model) plansAction(msg tea.KeyMsg) (next tea.Model, cmd tea.Cmd, ok bool) {
	key := msg.String()
	if index := strings.Index("123456", key); index >= 0 && len(key) == 1 {
		next, cmd = m.switchTab(index)
		return next, cmd, true
	}
	switch key {
	case "]":
		next, cmd = m.switchTab((m.tab + 1) % len(tabs))
		return next, cmd, true
	case "[":
		next, cmd = m.switchTab((m.tab + len(tabs) - 1) % len(tabs))
		return next, cmd, true
	}
	if m.planID() == 0 {
		return m, nil, false
	}
	if m.tab == proxiesTab {
		switch key {
		case "b":
			if m.mode == "direct" {
				m.mode = "backbone"
			} else {
				m.mode = "direct"
			}
			next, cmd = m.reloadTab(proxiesTab)
			return next, cmd, true
		case "/":
			next, cmd = m.startForm([][2]string{{"country codes (comma-separated, empty for all): ", strings.Join(m.countries, ",")}},
				func(m model, values []string) (model, tea.Cmd) {
					m.countries = nil
					for _, code := range strings.Split(values[0], ",") {
						if code = strings.TrimSpace(code); code != "" {
							m.countries = append(m.countries, strings.ToUpper(code))
						}
					}
					return m.reloadTab(proxiesTab)
				})
			return next, cmd, true
		case "c":
			next, cmd = m.copySelected()
			return next, cmd, true
		}
	}
	if key == "t" && (m.tab == bandwidthTab || m.tab == errorsTab || m.tab == activityTab) {
		m.ranges[m.tab] = (m.ranges[m.tab] + 1) % rangeCount
		next, cmd = m.reloadTab(m.tab)
		return next, cmd, true
	}
	if key == "/" && m.tab == activityTab {
		next, cmd = m.startForm([][2]string{
			{"search by exact IP or domain (empty for all): ", m.activitySearch},
			{"error reason ('*' for any error, empty for all): ", m.activityError},
		}, func(m model, values []string) (model, tea.Cmd) {
			m.activitySearch, m.activityError = values[0], values[1]
			return m.reloadTab(activityTab)
		})
		return next, cmd, true
	}
	if m.focus == 1 && (key == "left" || key == "right") {
		step := scrollStep
		if key == "left" {
			step = -step
		}
		m.hscroll[m.tab] = min(max(m.hscroll[m.tab]+step, 0), m.overflow())
		return m, nil, true
	}
	if m.focus == 1 && (m.tab == ipsTab || m.tab == subusersTab) {
		return m.accessAction(key)
	}
	return m, nil, false
}

// scrollStep is how many cells ←/→ scroll a wide table.
const scrollStep = 8

// overflow is how many cells the selected tab's table is wider than the
// panel, 0 when it fits or there is no table.
func (m model) overflow() int {
	t := m.focusable()
	if t == nil {
		return 0
	}
	return max(tableWidth(*t)-(m.width-sidebarWidth-2), 0)
}

// tableWidth is a table's full width: its columns plus the cell padding.
func tableWidth(t table.Model) int {
	width := 0
	for _, column := range t.Columns() {
		width += column.Width + columnGap
	}
	return width
}

// tableView renders the selected tab's table scrolled sideways by hscroll.
func (m model) tableView() string {
	view := m.focusable().View()
	offset := min(m.hscroll[m.tab], m.overflow())
	if offset == 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = ansi.TruncateLeft(line, offset, "")
	}
	return strings.Join(lines, "\n")
}

// switchTab moves to another tab, focuses its table (a tab without one,
// Bandwidth, leaves the focus on the sidebar) and loads it if needed.
func (m model) switchTab(tab int) (model, tea.Cmd) {
	m = m.switchScreen(plansScreen)
	m.tab = tab
	if t := m.focusable(); t != nil {
		m.focus = 1
		t.Focus()
	}
	return m, m.loadMissing()
}

func (m model) startForm(fields [][2]string, submit func(m model, values []string) (model, tea.Cmd)) (model, tea.Cmd) {
	m.form = &form{fields: fields, submit: submit}
	m.input.Prompt, m.input.Placeholder = fields[0][0], ""
	m.input.SetValue(fields[0][1])
	m.input.CursorEnd()
	return m, m.input.Focus()
}

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.form = nil
		m.input.Blur()
		return m, nil
	case "enter":
		f := m.form
		f.fields[f.step][1] = strings.TrimSpace(m.input.Value())
		if f.step++; f.step < len(f.fields) {
			m.input.Prompt = f.fields[f.step][0]
			m.input.SetValue(f.fields[f.step][1])
			m.input.CursorEnd()
			return m, nil
		}
		m.form = nil
		m.input.Blur()
		values := make([]string, len(f.fields))
		for i, field := range f.fields {
			values[i] = field[1]
		}
		return f.submit(m, values)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// reload refetches what the current screen shows; on the plans screen it
// drops the selected plan's cache first.
func (m model) reload() (model, tea.Cmd) {
	if m.screen != plansScreen || m.planID() == 0 {
		// The account on screen stays until the new one arrives.
		return m, m.loadAccount
	}
	delete(m.cache, m.planID())
	for key := range m.inflight {
		if strings.HasPrefix(key, strconv.Itoa(m.planID())+"|") {
			delete(m.inflight, key)
		}
	}
	m = m.applyCache()
	return m, m.loadMissing()
}

func (m *model) setProxies(msg tabLoaded) {
	m.proxyList = msg.data.([]webshare.Proxy)
	rows := make([]table.Row, 0, len(m.proxyList))
	for _, p := range m.proxyList {
		valid := "yes"
		if !p.Valid {
			valid = "no"
		}
		rows = append(rows, table.Row{app.ProxyHost(p), strconv.Itoa(p.Port), p.CountryCode, p.CityName, valid})
	}
	m.proxies.SetRows(rows)
}

func (m model) proxiesStatus() string {
	msg := m.shown[proxiesTab]
	if msg.truncated {
		return fmt.Sprintf("first %d proxies", len(m.proxyList))
	}
	return fmt.Sprintf("%d proxies", len(m.proxyList))
}

// cliCommand is the CLI equivalent of what the screen or tab shows.
func (m model) cliCommand() string {
	switch m.screen {
	case accountScreen:
		return "webshare account"
	case subscriptionScreen:
		return "webshare account && webshare plans list --all"
	}
	plan := " --plan " + strconv.Itoa(m.planID())
	switch m.tab {
	case proxiesTab:
		return m.proxiesCommand() + plan
	case bandwidthTab, errorsTab:
		if since := rangeSince(m.ranges[m.tab]); since != "" {
			return "webshare stats --since " + since + plan
		}
		return ""
	case activityTab:
		if command := m.activityCommand(); command != "" {
			return command + plan
		}
		return ""
	case ipsTab:
		return "webshare ipauth list" + plan
	}
	return "webshare subusers list" + plan
}

// verifiedProxyCount is the dashboard's threshold for a Verified plan in
// production (makeDefaultVerifiedProxyCount).
const verifiedProxyCount = 5000

// productName is the name the dashboard's Products menu gives a plan,
// following its getProxyPlanKey and makeProxyTabType.
func productName(p webshare.Plan) string {
	if p.ProxyType == webshare.ProxyTypeFree {
		return "Free"
	}
	subtype := p.ProxySubtype
	if len(p.RequiredSiteChecks) > 0 && subtype == webshare.SubtypeDatacenterAndISP {
		switch {
		case p.ProxyType == webshare.ProxyTypeShared && p.ProxyCount >= verifiedProxyCount:
			return "Verified"
		case p.ProxyType == webshare.ProxyTypeDedicated || p.ProxyType == webshare.ProxyTypeSemidedicated:
			subtype = webshare.SubtypeISP
		}
	}
	switch subtype {
	case webshare.SubtypeISP:
		return "Static Residential"
	case webshare.SubtypeResidential:
		return "Rotating Residential"
	}
	return "Proxy Server"
}

// proxiesCommand is the CLI command that lists what the proxies tab shows.
func (m model) proxiesCommand() string {
	command := "webshare proxies list"
	if m.mode != "direct" {
		command += " --mode " + m.mode
	}
	if len(m.countries) > 0 {
		command += " --country " + strings.ToLower(strings.Join(m.countries, ","))
	}
	return command
}

// copySelected copies the selected proxy's URL. The feedback names the proxy
// by address only: the URL carries its password.
func (m model) copySelected() (model, tea.Cmd) {
	cursor := m.proxies.Cursor()
	if cursor < 0 || cursor >= len(m.proxyList) {
		m.notices[proxiesTab] = "no proxy selected"
		return m, nil
	}
	p := m.proxyList[cursor]
	host := net.JoinHostPort(app.ProxyHost(p), strconv.Itoa(p.Port))
	proxyURL := (&url.URL{Scheme: "http", User: url.UserPassword(p.Username, p.Password), Host: host}).String()
	m.notices[proxiesTab] = "copying…"
	return m, copyToClipboard(proxyURL, "the proxy URL for "+host)
}

// clipboardDone reports a native clipboard copy; tool is empty when none
// was tried.
type clipboardDone struct {
	text, what, tool string
	err              error
}

// clipboardCommands are the native clipboard tools, tried in order.
var clipboardCommands = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// copyToClipboard copies text with the first native clipboard tool found,
// off the event loop so a hung tool cannot freeze the UI. Over SSH it tries
// none: the remote machine's clipboard is not the user's. what names the
// text in the feedback.
func copyToClipboard(text, what string) tea.Cmd {
	return func() tea.Msg {
		if os.Getenv("SSH_TTY") != "" {
			return clipboardDone{text: text, what: what}
		}
		for _, args := range clipboardCommands {
			path, err := exec.LookPath(args[0])
			if err != nil {
				continue
			}
			cmd := exec.Command(path, args[1:]...)
			cmd.Stdin = strings.NewReader(text)
			return clipboardDone{text: text, what: what, tool: args[0], err: cmd.Run()}
		}
		return clipboardDone{text: text, what: what}
	}
}

// copyFeedback is the notice for a finished copy. Without a working native
// tool it falls back to OSC 52, which asks the terminal to copy and cannot
// report whether it did.
func copyFeedback(msg clipboardDone) string {
	if msg.tool != "" && msg.err == nil {
		return "copied " + msg.what
	}
	// ponytail: written outside the renderer, fine for one short sequence.
	termenv.Copy(msg.text)
	if msg.tool != "" {
		return fmt.Sprintf("%s failed (%v); sent %s to the terminal through OSC 52 instead", msg.tool, msg.err, msg.what)
	}
	return "sent " + msg.what + " to the terminal through OSC 52 (the terminal must allow it)"
}

// keyValues renders an aligned key/value listing with dimmed keys.
func keyValues(pairs [][2]string) string {
	width := 0
	for _, pair := range pairs {
		width = max(width, len(pair[0]))
	}
	var b strings.Builder
	for _, pair := range pairs {
		key := fmt.Sprintf("%-*s", width+columnGap, pair[0])
		b.WriteString(margin + dimStyle.Render(key) + pair[1] + "\n")
	}
	return b.String()
}

// columns renders a static aligned table for the read-only views.
func columns(header []string, rows [][]string) string {
	if len(rows) == 0 {
		return margin + dimStyle.Render("(none)") + "\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, columnGap, ' ', 0)
	// The margin goes inside the first cell of every line, header included,
	// so it counts the same towards the first column's width.
	fmt.Fprintln(tw, margin+strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, margin+strings.Join(row, "\t"))
	}
	tw.Flush()
	// Style after aligning: escape codes would count towards column widths.
	head, rest, _ := strings.Cut(b.String(), "\n")
	return dimStyle.Render(head) + "\n" + rest
}

// titledBox is a rounded box with its title in the top border, coloured
// when focused. A height of 0 fits the content.
func titledBox(title, body string, width, height int, focused bool) string {
	border := lipgloss.RoundedBorder()
	box := borderStyle.BorderTop(false).Width(max(width-2, 1))
	edge := lipgloss.NewStyle()
	if focused {
		box = box.BorderForeground(focusedColor)
		edge = edge.Foreground(focusedColor)
	}
	if height > 0 {
		box = box.Height(max(height-2, 1))
	}
	top := edge.Render(border.TopLeft+border.Top+" ") + title + edge.Render(" "+
		strings.Repeat(border.Top, max(width-lipgloss.Width(title)-5, 0))+border.TopRight)
	return top + "\n" + box.Render(strings.TrimSuffix(body, "\n"))
}

var (
	borderStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	focusedColor = lipgloss.Color("6")
	dimStyle     = lipgloss.NewStyle().Faint(true)
	selectStyle  = lipgloss.NewStyle().Bold(true).Reverse(true)
	keyStyle     = lipgloss.NewStyle().Foreground(focusedColor)
)

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	// The keybindings bar takes the last row.
	height := max(m.height-1, 3)
	var view string
	switch {
	case m.showHelp:
		view = lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, helpView())
	case m.screen == accountScreen:
		view = m.accountView(m.width, height)
	case m.screen == subscriptionScreen:
		view = m.subscriptionView(m.width, height)
	default:
		view = m.plansView(height)
	}
	return view + "\n" + m.keybindings()
}

// pinStatus puts status in the bottom-left corner under the feedback line,
// indented like the table's cells.
func (m model) pinStatus(height int, top, status string) string {
	bottom := m.bottomLine() + "\n" + margin + dimStyle.Render(status)
	filler := max(height-2-lipgloss.Height(top)-lipgloss.Height(bottom), 0)
	return top + "\n" + strings.Repeat("\n", filler) + bottom
}

func (m model) plansView(height int) string {
	var items string
	for i, p := range m.activePlans {
		line := " " + productName(p)
		if i == m.plan {
			line = selectStyle.Render(line)
		}
		items += line + "\n"
	}
	switch {
	case m.accountErr != nil:
		items += "\nerror: " + m.accountErr.Error()
	case m.account == nil:
		// Indented like the product names.
		items = " " + shimmer(m.frame)
	case len(m.activePlans) == 0:
		items = dimStyle.Render("no active products")
	}
	sidebar := titledBox("Products", items, sidebarWidth, height, m.focus == 0)

	names := make([]string, len(tabs))
	for i, name := range tabs {
		if i == m.tab {
			names[i] = keyStyle.Bold(true).Render(name)
		} else {
			names[i] = dimStyle.Render(name)
		}
	}
	var body string
	shown := m.shown[m.tab]
	switch {
	case m.planID() == 0:
	case shown == nil:
		body = margin + shimmer(m.frame)
	case shown.err != nil:
		body = margin + "error: " + shown.err.Error()
	case m.tab == proxiesTab:
		body = m.pinStatus(height, m.tableView(), m.proxiesStatus()+" · mode "+m.mode)
	case m.tab == activityTab:
		body = m.pinStatus(height, m.tableView(), m.activityStatus())
	case m.tab == bandwidthTab:
		body = m.bandwidthView(m.width - sidebarWidth - 2)
	case m.tab == errorsTab:
		body = m.errorsSummary() + "\n" + m.tableView() + "\n" + m.bottomLine()
	default:
		body = m.tableView() + "\n" + m.bottomLine()
	}
	// Wide tables are clipped at the border instead of wrapping.
	body = lipgloss.NewStyle().MaxWidth(m.width - sidebarWidth - 2).Render(body)
	main := titledBox(strings.Join(names, dimStyle.Render(" │ ")), body, m.width-sidebarWidth, height, m.focus == 1)
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, main)
}

// bottomLine is the view's last line: the open form, the pending
// confirmation or the last action's feedback.
func (m model) bottomLine() string {
	switch {
	case m.form != nil:
		return margin + m.input.View()
	case m.confirm != nil:
		return margin + m.confirm.question + " " + keyStyle.Render("y/n")
	}
	return margin + m.notices[m.noticeTarget()]
}

// allKeybindings is the full list shown by "?", grouped by where they work.
var allKeybindings = []struct {
	group    string
	bindings [][2]string
}{
	{"Global", [][2]string{
		{"↑/↓ or k/j", "move between products, or rows when focused"},
		{"tab", "focus the table / back to the sidebar"},
		{"←/→", "scroll a table wider than the panel sideways"},
		{"[ ] or 1-6", "switch tab and focus its table"},
		{"a", "account, again or esc to go back"},
		{"s", "subscription and plan history, again or esc to go back"},
		{"r", "refresh"},
		{"?", "show or hide this list"},
		{"q, ctrl+c", "quit"},
	}},
	{"Proxies", [][2]string{
		{"b", "switch between direct and backbone mode"},
		{"/", "filter by country codes"},
		{"c", "copy the selected proxy URL"},
		{"pgup/pgdown", "page through the table"},
		{"home/end or g/G", "first or last row"},
	}},
	{"Bandwidth, Errors and Activity", [][2]string{
		{"t", "time range: last 24 hours, last 7 days, this billing cycle"},
	}},
	{"Activity", [][2]string{
		{"/", "search by IP or domain and filter by error reason"},
	}},
	{"Authorized IPs", [][2]string{
		{"n", "authorize an IP, empty for this machine's"},
		{"d", "remove the selected IP"},
	}},
	{"Sub-users", [][2]string{
		{"n", "new sub-user"},
		{"e", "edit the selected sub-user"},
		{"d", "delete the selected sub-user"},
	}},
	{"Account", [][2]string{
		{"e", "edit name and timezone"},
		{"space/enter", "turn the selected email notification on or off"},
	}},
	{"Forms and confirmations", [][2]string{
		{"enter", "next field, or save on the last one"},
		{"esc", "cancel"},
		{"y", "confirm, any other key cancels"},
	}},
}

func helpView() string {
	var b strings.Builder
	for i, group := range allKeybindings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(group.group) + "\n")
		width := 0
		for _, binding := range group.bindings {
			width = max(width, lipgloss.Width(binding[0]))
		}
		for _, binding := range group.bindings {
			key := binding[0] + strings.Repeat(" ", width-lipgloss.Width(binding[0])+columnGap)
			b.WriteString("  " + keyStyle.Render(key) + binding[1] + "\n")
		}
	}
	return borderStyle.BorderForeground(focusedColor).Padding(0, 2).Render(strings.TrimSuffix(b.String(), "\n"))
}

// keybindings renders the bottom bar, lazygit style: only the keys that work
// where the focus is, and the CLI equivalent on the right.
func (m model) keybindings() string {
	var bindings [][2]string
	switch {
	case m.showHelp:
		bindings = [][2]string{{"Close", "esc"}}
	case m.form != nil:
		bindings = [][2]string{{"Next/save", "enter"}, {"Cancel", "esc"}}
	case m.confirm != nil:
		bindings = [][2]string{{"Confirm", "y"}, {"Cancel", "any key"}}
	case m.focus == 1:
		bindings = [][2]string{{"Move", "↑/↓"}}
		if m.screen == plansScreen && m.overflow() > 0 {
			bindings = append(bindings, [2]string{"Scroll", "←/→"})
		}
		bindings = append(bindings, [2]string{"Back", "tab"})
	case m.screen == plansScreen:
		bindings = [][2]string{{"Products", "↑/↓"}, {"Tabs", "[ ]"}}
		if m.focusable() != nil {
			bindings = append(bindings, [2]string{"Focus", "tab"})
		}
	default:
		bindings = [][2]string{{"Back", "esc"}}
		if m.focusable() != nil {
			bindings = append(bindings, [2]string{"Focus", "tab"})
		}
	}
	if m.form == nil && m.confirm == nil && !m.showHelp {
		bindings = append(bindings, [2]string{"Refresh", "r"})
		switch {
		case m.screen == accountScreen:
			bindings = append(bindings, [2]string{"Edit profile", "e"})
			if m.focus == 1 {
				bindings = append(bindings, [2]string{"Toggle", "space"})
			}
		case m.screen != plansScreen:
		case m.tab == proxiesTab:
			bindings = append(bindings, [2]string{"Direct/backbone", "b"}, [2]string{"Country", "/"}, [2]string{"Copy URL", "c"})
		case m.tab == bandwidthTab || m.tab == errorsTab:
			bindings = append(bindings, [2]string{"Range", "t"})
		case m.tab == activityTab:
			bindings = append(bindings, [2]string{"Range", "t"}, [2]string{"Filter", "/"})
		case m.tab == ipsTab && m.focus == 1:
			bindings = append(bindings, [2]string{"Add IP", "n"}, [2]string{"Remove", "d"})
		case m.tab == subusersTab && m.focus == 1:
			bindings = append(bindings, [2]string{"New", "n"}, [2]string{"Edit", "e"}, [2]string{"Delete", "d"})
		}
		bindings = append(bindings, [2]string{"Account", "a"}, [2]string{"Subscription", "s"},
			[2]string{"Quit", "q"}, [2]string{"Keybindings", "?"})
	}
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		parts = append(parts, b[0]+": "+keyStyle.Render(b[1]))
	}
	bar := strings.Join(parts, dimStyle.Render(" | "))
	// The CLI equivalent sits on the right when it fits.
	cli := dimStyle.Render("cli: " + m.cliCommand())
	if gap := m.width - lipgloss.Width(bar) - lipgloss.Width(cli); gap > 0 && !m.showHelp && m.cliCommand() != "" && (m.screen != plansScreen || m.planID() != 0) {
		bar += strings.Repeat(" ", gap) + cli
	}
	return bar
}
