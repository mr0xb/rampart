package output

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ToggleFunc enables/disables the item with the given id and returns an error
// if the API call failed.
type ToggleFunc func(id string, enable bool) error

// TableOpts configures an interactive table. If OnToggle is set (and IDColumn
// and EnabledCol are valid), pressing space toggles the selected row.
type TableOpts struct {
	Title      string
	Columns    []string
	Rows       [][]string
	IDColumn   int // index of the column holding the item id, -1 if none
	EnabledCol int // index of the yes/no "enabled" column, -1 if not toggleable
	OnToggle   ToggleFunc
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).Padding(0, 1)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 1)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Padding(0, 1)
	borderStyle = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
)

// RunInteractiveTable opens a full-screen navigable table.
func RunInteractiveTable(o TableOpts) error {
	cols := make([]table.Column, len(o.Columns))
	for i, h := range o.Columns {
		w := len(h)
		for _, r := range o.Rows {
			if l := len(r[i]); l > w {
				w = l
			}
		}
		if w > 40 {
			w = 40
		}
		cols[i] = table.Column{Title: h, Width: w}
	}
	rows := make([]table.Row, len(o.Rows))
	for i, r := range o.Rows {
		rows[i] = table.Row(r)
	}
	height := len(rows) + 1
	if height > 20 {
		height = 20
	}
	if height < 3 {
		height = 3
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).Bold(true)
	s.Selected = s.Selected.Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Bold(false)
	t.SetStyles(s)

	_, err := tea.NewProgram(tuiModel{opts: o, tbl: t}, tea.WithAltScreen()).Run()
	return err
}

type tuiModel struct {
	opts   TableOpts
	tbl    table.Model
	status string
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h := msg.Height - 7
		if h > len(m.opts.Rows)+1 {
			h = len(m.opts.Rows) + 1
		}
		if h < 3 {
			h = 3
		}
		m.tbl.SetHeight(h)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case " ":
			m.toggleSelected()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m *tuiModel) toggleSelected() {
	if m.opts.OnToggle == nil || m.opts.EnabledCol < 0 || m.opts.IDColumn < 0 {
		return
	}
	row := m.tbl.SelectedRow()
	if row == nil {
		return
	}
	id := row[m.opts.IDColumn]
	enable := row[m.opts.EnabledCol] != "yes"
	if err := m.opts.OnToggle(id, enable); err != nil {
		m.status = errStyle.Render("error: " + err.Error())
		return
	}
	rows := m.tbl.Rows()
	rows[m.tbl.Cursor()][m.opts.EnabledCol] = YesNo(enable)
	m.tbl.SetRows(rows)
	verb := "disabled"
	if enable {
		verb = "enabled"
	}
	m.status = okStyle.Render(fmt.Sprintf("%s %s", verb, id))
}

func (m tuiModel) View() string {
	help := "↑/↓ move · q quit"
	if m.opts.OnToggle != nil {
		help = "↑/↓ move · space enable/disable · q quit"
	}
	return titleStyle.Render(m.opts.Title) + "\n" +
		borderStyle.Render(m.tbl.View()) + "\n" +
		m.status + "\n" +
		helpStyle.Render(help)
}
