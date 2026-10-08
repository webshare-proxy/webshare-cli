// Package tui is the interactive front-end launched by `webshare ui`. It reads
// through internal/app and the SDK, never through the cobra commands.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/webshare-proxy/webshare-cli/internal/app"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

// proxyLimit caps the proxies panel like `webshare proxies list` does.
const proxyLimit = 100

const sidebarWidth = 22

// columnGap is the space between columns in every panel.
const columnGap = 4

var panels = []string{"Account & plans", "Proxies", "Usage", "IP auth & sub-users"}

const (
	accountPanel = iota
	proxiesPanel
	usagePanel
	accessPanel
)

// Run draws the UI until the user quits or ctx is cancelled.
func Run(ctx context.Context, client *webshare.Client) error {
	_, err := tea.NewProgram(newModel(ctx, client), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) {
		return ctx.Err()
	}
	return err
}

type proxiesLoaded struct {
	proxies   []webshare.Proxy
	truncated bool
	err       error
}

type accountLoaded struct {
	account *app.Account
	plans   []webshare.Plan
	err     error
}

type usageLoaded struct {
	stats *webshare.AggregateStats
	err   error
}

type accessLoaded struct {
	auths    []webshare.IPAuthorization
	subusers []webshare.Subuser
	err      error
}

type model struct {
	ctx    context.Context
	client *webshare.Client

	selected    int
	mainFocused bool
	width       int
	height      int

	proxies       table.Model
	proxiesStatus string

	// Rendered bodies of the read-only panels, keyed by panel index.
	bodies map[int]string
}

func newModel(ctx context.Context, client *webshare.Client) model {
	proxies := table.New(table.WithColumns([]table.Column{
		{Title: "ADDRESS", Width: 18},
		{Title: "PORT", Width: 6},
		{Title: "COUNTRY", Width: 8},
		{Title: "CITY", Width: 16},
		{Title: "VALID", Width: 5},
	}))
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Padding(0, columnGap/2)
	styles.Cell = styles.Cell.Padding(0, columnGap/2)
	proxies.SetStyles(styles)
	return model{ctx: ctx, client: client, proxies: proxies, proxiesStatus: "loading…", bodies: map[int]string{}}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadProxies, m.loadAccount, m.loadUsage, m.loadAccess)
}

func (m model) loadAccount() tea.Msg {
	account, err := app.GetAccount(m.ctx, m.client)
	if err != nil {
		return accountLoaded{err: err}
	}
	plans, err := app.ListPlans(m.ctx, m.client, false)
	return accountLoaded{account: account, plans: plans, err: err}
}

func (m model) loadUsage() tea.Msg {
	since := time.Now().Add(-24 * time.Hour)
	stats, err := m.client.Stats.Aggregate(m.ctx, webshare.StatsListParams{TimestampGTE: &since})
	return usageLoaded{stats: stats, err: err}
}

func (m model) loadAccess() tea.Msg {
	var msg accessLoaded
	for auth, err := range m.client.IPAuthorizations.ListAll(m.ctx, webshare.IPAuthorizationListParams{}) {
		if err != nil {
			return accessLoaded{err: err}
		}
		msg.auths = append(msg.auths, auth)
	}
	for subuser, err := range m.client.Subusers.ListAll(m.ctx, webshare.SubuserListParams{}) {
		if err != nil {
			return accessLoaded{err: err}
		}
		msg.subusers = append(msg.subusers, subuser)
	}
	return msg
}

func (m model) loadProxies() tea.Msg {
	// ponytail: direct mode only, residential plans need a backbone toggle.
	params := webshare.ProxyListParams{Mode: webshare.ConnectionMode("direct")}
	proxies, truncated, err := app.ListProxies(m.ctx, m.client, params, proxyLimit)
	return proxiesLoaded{proxies: proxies, truncated: truncated, err: err}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.proxies.SetWidth(max(m.width-sidebarWidth-4, 0))
		m.proxies.SetHeight(max(m.height-4, 1))
		return m, nil
	case proxiesLoaded:
		m.proxiesStatus = proxiesStatus(msg)
		rows := make([]table.Row, 0, len(msg.proxies))
		for _, p := range msg.proxies {
			valid := "yes"
			if !p.Valid {
				valid = "no"
			}
			rows = append(rows, table.Row{app.ProxyHost(p), strconv.Itoa(p.Port), p.CountryCode, p.CityName, valid})
		}
		m.proxies.SetRows(rows)
		return m, nil
	case accountLoaded:
		m.bodies[accountPanel] = accountBody(msg)
		return m, nil
	case usageLoaded:
		m.bodies[usagePanel] = usageBody(msg)
		return m, nil
	case accessLoaded:
		m.bodies[accessPanel] = accessBody(msg)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.mainFocused = !m.mainFocused && m.selected == proxiesPanel
			if m.mainFocused {
				m.proxies.Focus()
			} else {
				m.proxies.Blur()
			}
			return m, nil
		}
		if m.mainFocused {
			var cmd tea.Cmd
			m.proxies, cmd = m.proxies.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "j", "down":
			m.selected = (m.selected + 1) % len(panels)
		case "k", "up":
			m.selected = (m.selected + len(panels) - 1) % len(panels)
		}
	}
	return m, nil
}

func proxiesStatus(msg proxiesLoaded) string {
	switch {
	case msg.err != nil:
		return "error: " + msg.err.Error()
	case msg.truncated:
		return fmt.Sprintf("first %d proxies", len(msg.proxies))
	default:
		return fmt.Sprintf("%d proxies", len(msg.proxies))
	}
}

func accountBody(msg accountLoaded) string {
	if msg.err != nil {
		return "error: " + msg.err.Error()
	}
	profile, subscription := msg.account.Profile, msg.account.Subscription
	var b strings.Builder
	b.WriteString(keyValues([][2]string{
		{"Email", profile.Email},
		{"Member since", profile.CreatedAt.Format("2006-01-02")},
		{"Free credits", fmt.Sprintf("$%.2f", subscription.FreeCredits)},
		{"Term", string(subscription.Term)},
		{"Renews", subscription.EndDate.Format("2006-01-02")},
		{"Auto-renewal", strconv.FormatBool(subscription.RenewalsEnabled)},
		{"Throttled", strconv.FormatBool(subscription.Throttled)},
	}))
	b.WriteString("\nActive plans\n")
	rows := make([][]string, 0, len(msg.plans))
	for _, p := range msg.plans {
		rows = append(rows, []string{
			strconv.Itoa(p.ID), string(p.ProxyType), string(p.ProxySubtype),
			strconv.Itoa(p.ProxyCount), output.Bandwidth(p.BandwidthLimit),
		})
	}
	b.WriteString(columns([]string{"ID", "TYPE", "SUBTYPE", "PROXIES", "BANDWIDTH"}, rows))
	return b.String()
}

func usageBody(msg usageLoaded) string {
	if msg.err != nil {
		return "error: " + msg.err.Error()
	}
	stats := msg.stats
	successRate := "-"
	if stats.RequestsTotal > 0 {
		successRate = fmt.Sprintf("%.1f%%", 100*float64(stats.RequestsSuccessful)/float64(stats.RequestsTotal))
	}
	pairs := [][2]string{
		{"Requests", strconv.FormatInt(stats.RequestsTotal, 10)},
		{"Successful", fmt.Sprintf("%d (%s)", stats.RequestsSuccessful, successRate)},
		{"Failed", strconv.FormatInt(stats.RequestsFailed, 10)},
		{"Bandwidth used", output.Bytes(stats.BandwidthTotal)},
		{"Bandwidth projected", output.Bytes(stats.BandwidthProjected)},
		{"Unique proxies used", strconv.Itoa(stats.NumberOfProxiesUsed)},
	}
	for _, reason := range stats.ErrorReasons {
		pairs = append(pairs, [2]string{"Error " + reason.Reason, fmt.Sprintf("%d: %s", reason.Count, reason.HowToFix)})
	}
	var b strings.Builder
	b.WriteString("Last 24 hours\n\n")
	b.WriteString(keyValues(pairs))
	return b.String()
}

func accessBody(msg accessLoaded) string {
	if msg.err != nil {
		return "error: " + msg.err.Error()
	}
	authRows := make([][]string, 0, len(msg.auths))
	for _, a := range msg.auths {
		lastUsed := "never"
		if a.LastUsedAt != nil {
			lastUsed = a.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		authRows = append(authRows, []string{a.IPAddress, a.CreatedAt.Local().Format("2006-01-02"), lastUsed})
	}
	subuserRows := make([][]string, 0, len(msg.subusers))
	for _, s := range msg.subusers {
		limit := "unlimited"
		if s.ProxyLimit > 0 {
			limit = fmt.Sprintf("%g GB", s.ProxyLimit)
		}
		subuserRows = append(subuserRows, []string{
			s.Label, limit, strconv.Itoa(s.MaxThreadCount), output.Bytes(s.AggregateStats.BandwidthTotal),
		})
	}
	return "Authorized IPs\n" + columns([]string{"IP", "ADDED", "LAST USED"}, authRows) +
		"\nSub-users\n" + columns([]string{"LABEL", "BANDWIDTH LIMIT", "MAX THREADS", "USED"}, subuserRows)
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
		b.WriteString(dimStyle.Render(key) + pair[1] + "\n")
	}
	return b.String()
}

// columns renders a static aligned table for the read-only panels.
func columns(header []string, rows [][]string) string {
	if len(rows) == 0 {
		return dimStyle.Render("(none)") + "\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, columnGap, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
	// Style after aligning: escape codes would count towards column widths.
	head, rest, _ := strings.Cut(b.String(), "\n")
	return dimStyle.Render(head) + "\n" + rest
}

var (
	borderStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	focusedColor = lipgloss.Color("6")
	dimStyle     = lipgloss.NewStyle().Faint(true)
	selectStyle  = lipgloss.NewStyle().Bold(true).Reverse(true)
)

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	height := max(m.height-2, 1)

	items := ""
	for i, name := range panels {
		line := " " + name
		if i == m.selected {
			line = selectStyle.Render(line)
		}
		items += line + "\n"
	}
	sidebar := borderStyle.Width(sidebarWidth - 2).Height(height)
	if !m.mainFocused {
		sidebar = sidebar.BorderForeground(focusedColor)
	}

	var body string
	if m.selected == proxiesPanel {
		body = m.proxies.View() + "\n" + dimStyle.Render(m.proxiesStatus+" · tab to focus · q to quit")
	} else if text, ok := m.bodies[m.selected]; ok {
		body = text + "\n" + dimStyle.Render("q to quit")
	} else {
		body = dimStyle.Render("loading…")
	}
	main := borderStyle.Width(max(m.width-sidebarWidth-2, 1)).Height(height)
	if m.mainFocused {
		main = main.BorderForeground(focusedColor)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar.Render(items), main.Render(body))
}
