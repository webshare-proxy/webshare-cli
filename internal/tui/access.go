package tui

import (
	"fmt"
	"net"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func (m *model) setIPs(msg tabLoaded) {
	m.auths = msg.data.([]webshare.IPAuthorization)
	ipRows := make([]table.Row, 0, len(m.auths))
	for _, a := range m.auths {
		lastUsed := "never"
		if a.LastUsedAt != nil {
			lastUsed = a.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		ipRows = append(ipRows, table.Row{a.IPAddress, a.CreatedAt.Local().Format("2006-01-02"), lastUsed})
	}
	m.ips.SetRows(ipRows)
}

func (m *model) setSubusers(msg tabLoaded) {
	m.subuserList = msg.data.([]webshare.Subuser)
	subuserRows := make([]table.Row, 0, len(m.subuserList))
	for _, s := range m.subuserList {
		subuserRows = append(subuserRows, table.Row{
			s.Label, bandwidthLimit(s.ProxyLimit), strconv.Itoa(s.MaxThreadCount), output.Bytes(s.AggregateStats.BandwidthTotal),
		})
	}
	m.subusers.SetRows(subuserRows)
}

func bandwidthLimit(gb float64) string {
	if gb > 0 {
		return fmt.Sprintf("%g GB", gb)
	}
	return "unlimited"
}

// accessAction handles the write keys of the focused IPs or sub-users
// table; ok is false when the key is not one of them.
func (m model) accessAction(key string) (next tea.Model, cmd tea.Cmd, ok bool) {
	planID := m.planID()
	switch {
	case m.tab == ipsTab && key == "n":
		next, cmd = m.startForm([][2]string{{"IP to authorize (empty for this machine's public IP): ", ""}}, addIP)
	case m.tab == ipsTab && key == "d":
		cursor := m.ips.Cursor()
		if cursor < 0 || cursor >= len(m.auths) {
			return m, nil, true
		}
		a := m.auths[cursor]
		m.confirm = &confirmation{"remove IP authorization " + a.IPAddress + "?", func() tea.Msg {
			err := m.client.IPAuthorizations.Delete(m.ctx, a.ID, webshare.IPAuthorizationGetParams{PlanID: webshare.Int(planID)})
			return actionDone{planID: planID, target: ipsTab, notice: "removed " + a.IPAddress, err: err}
		}}
		next = m
	case m.tab == subusersTab && key == "n":
		next, cmd = m.startForm(subuserFields("", "", ""), createSubuser)
	case m.tab == subusersTab && (key == "e" || key == "d"):
		cursor := m.subusers.Cursor()
		if cursor < 0 || cursor >= len(m.subuserList) {
			return m, nil, true
		}
		s := m.subuserList[cursor]
		if key == "e" {
			fields := subuserFields(s.Label, strconv.FormatFloat(s.ProxyLimit, 'g', -1, 64), strconv.Itoa(s.MaxThreadCount))
			next, cmd = m.startForm(fields, func(m model, values []string) (model, tea.Cmd) {
				return m.updateSubuser(s.ID, values)
			})
			break
		}
		m.confirm = &confirmation{fmt.Sprintf("delete sub-user %s?", s.Label), func() tea.Msg {
			err := m.client.Subusers.Delete(m.ctx, s.ID, webshare.SubuserGetParams{PlanID: webshare.Int(planID)})
			return actionDone{planID: planID, target: subusersTab, notice: "deleted sub-user " + s.Label, err: err}
		}}
		next = m
	default:
		return m, nil, false
	}
	return next, cmd, true
}

func addIP(m model, values []string) (model, tea.Cmd) {
	ip := values[0]
	if ip != "" && net.ParseIP(ip) == nil {
		m.notices[ipsTab] = fmt.Sprintf("error: %q is not a valid IP address", ip)
		return m, nil
	}
	m.notices[m.tab] = "working…"
	planID := m.planID()
	return m, func() tea.Msg {
		if ip == "" {
			result, err := m.client.IPAuthorizations.WhatsMyIP(m.ctx)
			if err != nil {
				return actionDone{planID: planID, target: ipsTab, err: fmt.Errorf("detecting your public IP: %w", err)}
			}
			ip = result.IPAddress
		}
		auth, err := m.client.IPAuthorizations.Create(m.ctx, webshare.IPAuthorizationCreateParams{IPAddress: ip, PlanID: webshare.Int(planID)})
		if err != nil {
			return actionDone{planID: planID, target: ipsTab, err: err}
		}
		return actionDone{planID: planID, target: ipsTab, notice: "authorized " + auth.IPAddress}
	}
}

func subuserFields(label, bandwidth, threads string) [][2]string {
	return [][2]string{
		{"label: ", label},
		{"bandwidth limit in GB (0 for unlimited): ", bandwidth},
		{"max threads (empty for the default): ", threads},
	}
}

// subuserLimits parses the bandwidth and thread fields; empty leaves them nil.
func subuserLimits(values []string) (bandwidth *float64, threads *int, err error) {
	if values[1] != "" {
		gb, err := strconv.ParseFloat(values[1], 64)
		if err != nil || gb < 0 {
			return nil, nil, fmt.Errorf("bandwidth must be a number of GB, got %q", values[1])
		}
		bandwidth = &gb
	}
	if values[2] != "" {
		n, err := strconv.Atoi(values[2])
		if err != nil || n < 0 {
			return nil, nil, fmt.Errorf("max threads must be a whole number, got %q", values[2])
		}
		threads = &n
	}
	return bandwidth, threads, nil
}

func createSubuser(m model, values []string) (model, tea.Cmd) {
	bandwidth, threads, err := subuserLimits(values)
	if values[0] == "" {
		err = fmt.Errorf("label is required")
	}
	if err != nil {
		m.notices[m.tab] = "error: " + err.Error()
		return m, nil
	}
	m.notices[m.tab] = "working…"
	planID := m.planID()
	return m, func() tea.Msg {
		params := webshare.SubuserCreateParams{PlanID: webshare.Int(planID), Label: values[0], ProxyLimit: bandwidth, MaxThreadCount: threads}
		s, err := m.client.Subusers.Create(m.ctx, params)
		if err != nil {
			return actionDone{planID: planID, target: subusersTab, err: err}
		}
		return actionDone{planID: planID, target: subusersTab, notice: "created sub-user " + s.Label}
	}
}

func (m model) updateSubuser(id int, values []string) (model, tea.Cmd) {
	bandwidth, threads, err := subuserLimits(values)
	if values[0] == "" {
		err = fmt.Errorf("label is required")
	}
	if err != nil {
		m.notices[m.tab] = "error: " + err.Error()
		return m, nil
	}
	m.notices[m.tab] = "working…"
	planID := m.planID()
	return m, func() tea.Msg {
		params := webshare.SubuserUpdateParams{PlanID: webshare.Int(planID), Label: webshare.String(values[0]), ProxyLimit: bandwidth, MaxThreadCount: threads}
		s, err := m.client.Subusers.Update(m.ctx, id, params)
		if err != nil {
			return actionDone{planID: planID, target: subusersTab, err: err}
		}
		return actionDone{planID: planID, target: subusersTab, notice: "updated sub-user " + s.Label}
	}
}
