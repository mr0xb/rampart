package output

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ToggleFunc enables/disables the item with the given id and returns an error
// if the API call failed.
type ToggleFunc func(id string, enable bool) error

// EditFunc persists edited field values for the item with the given id. The
// map is keyed by EditField.Key and holds only the fields the form exposes.
type EditFunc func(id string, values map[string]string) error

// FieldKind selects how an EditField is rendered and edited.
type FieldKind int

const (
	// FieldText is a free-text input.
	FieldText FieldKind = iota
	// FieldChoice cycles through Options with ←/→.
	FieldChoice
)

// EditField describes one editable field of the selected row.
type EditField struct {
	Label   string
	Key     string // key used in the values map handed to OnEdit
	Column  int    // table column to refresh after a save, -1 if not shown
	Kind    FieldKind
	Options []string // FieldChoice only
	// Validate rejects a value before it is sent to the API. Optional.
	Validate func(string) error
}

// TableOpts configures an interactive table. If OnToggle is set (and IDColumn
// and EnabledCol are valid), pressing space toggles the selected row. If
// OnEdit and EditFields are set, pressing "e" opens an edit form for the
// selected row.
type TableOpts struct {
	Title      string
	Columns    []string
	Rows       [][]string
	IDColumn   int // index of the column holding the item id, -1 if none
	EnabledCol int // index of the yes/no "enabled" column, -1 if not toggleable
	OnToggle   ToggleFunc
	EditFields []EditField
	OnEdit     EditFunc
	// EditGuard rejects editing a row (e.g. predefined entries the controller
	// will not accept changes to). Optional; nil allows every row.
	EditGuard   func(row []string) error
	StartInEdit bool // open the form on the first row immediately
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).Padding(0, 1)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 1)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Padding(0, 1)
	borderStyle = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Width(10).Align(lipgloss.Right).PaddingRight(1)
	focusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	choiceStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
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

	m := tuiModel{opts: o, tbl: t}
	if o.StartInEdit {
		m.beginEdit()
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type tuiModel struct {
	opts   TableOpts
	tbl    table.Model
	status string

	// edit form state; nil when not editing
	form *editForm
}

type editForm struct {
	id      string
	inputs  []textinput.Model // FieldText fields only; zero value for others
	options [][]string        // FieldChoice fields only; per-row option list
	choice  []int             // index into options[i]
	focus   int
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
		if m.form != nil {
			return m.updateForm(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case " ":
			m.toggleSelected()
			return m, nil
		case "e", "enter":
			m.beginEdit()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m *tuiModel) canEdit() bool {
	return m.opts.OnEdit != nil && len(m.opts.EditFields) > 0 && m.opts.IDColumn >= 0
}

// beginEdit opens the edit form seeded from the selected row.
func (m *tuiModel) beginEdit() {
	if !m.canEdit() {
		return
	}
	row := m.tbl.SelectedRow()
	if row == nil {
		return
	}
	if m.opts.EditGuard != nil {
		if err := m.opts.EditGuard(row); err != nil {
			m.status = errStyle.Render("cannot edit: " + err.Error())
			return
		}
	}
	f := &editForm{
		id:      row[m.opts.IDColumn],
		inputs:  make([]textinput.Model, len(m.opts.EditFields)),
		options: make([][]string, len(m.opts.EditFields)),
		choice:  make([]int, len(m.opts.EditFields)),
	}
	for i, fld := range m.opts.EditFields {
		cur := ""
		if fld.Column >= 0 && fld.Column < len(row) {
			cur = row[fld.Column]
			if cur == "-" { // orDash placeholder for an empty value
				cur = ""
			}
		}
		switch fld.Kind {
		case FieldChoice:
			// A value the controller reports but Options does not list (an
			// unknown ruleset, a zone from another site) is kept as the first
			// option, so saving the form cannot silently rewrite it.
			opts := fld.Options
			if cur != "" && indexOf(opts, cur) < 0 {
				opts = append([]string{cur}, opts...)
			}
			f.options[i] = opts
			f.choice[i] = max0(indexOf(opts, cur))
		default:
			in := textinput.New()
			in.SetValue(cur)
			in.CharLimit = 128
			in.Width = 32
			in.Prompt = ""
			f.inputs[i] = in
		}
	}
	m.form = f
	m.focusField(0)
	m.status = ""
}

// focusField moves keyboard focus to field i, blurring the rest.
func (m *tuiModel) focusField(i int) {
	if m.form == nil || len(m.opts.EditFields) == 0 {
		return
	}
	n := len(m.opts.EditFields)
	i = ((i % n) + n) % n // wrap in both directions
	m.form.focus = i
	for j := range m.opts.EditFields {
		if m.opts.EditFields[j].Kind != FieldText {
			continue
		}
		if j == i {
			m.form.inputs[j].Focus()
		} else {
			m.form.inputs[j].Blur()
		}
	}
}

func (m tuiModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fld := m.opts.EditFields[m.form.focus]
	switch msg.String() {
	case "esc", "ctrl+c":
		m.form = nil
		m.status = helpStyle.Render("edit cancelled")
		return m, nil
	case "tab", "down":
		m.focusField(m.form.focus + 1)
		return m, nil
	case "shift+tab", "up":
		m.focusField(m.form.focus - 1)
		return m, nil
	case "enter":
		m.save()
		return m, nil
	case "left", "right":
		if fld.Kind == FieldChoice && len(m.form.options[m.form.focus]) > 0 {
			d := 1
			if msg.String() == "left" {
				d = -1
			}
			n := len(m.form.options[m.form.focus])
			m.form.choice[m.form.focus] = ((m.form.choice[m.form.focus]+d)%n + n) % n
			return m, nil
		}
	}
	if fld.Kind == FieldText {
		var cmd tea.Cmd
		m.form.inputs[m.form.focus], cmd = m.form.inputs[m.form.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

// value returns the current form value of field i.
func (m *tuiModel) value(i int) string {
	fld := m.opts.EditFields[i]
	if fld.Kind == FieldChoice {
		if len(m.form.options[i]) == 0 {
			return ""
		}
		return m.form.options[i][m.form.choice[i]]
	}
	return strings.TrimSpace(m.form.inputs[i].Value())
}

// save validates the form, calls OnEdit and folds the new values back into the
// table row. The form stays open on validation or API failure.
func (m *tuiModel) save() {
	values := make(map[string]string, len(m.opts.EditFields))
	for i, fld := range m.opts.EditFields {
		v := m.value(i)
		if fld.Validate != nil {
			if err := fld.Validate(v); err != nil {
				m.status = errStyle.Render(fmt.Sprintf("%s: %v", fld.Label, err))
				m.focusField(i)
				return
			}
		}
		values[fld.Key] = v
	}
	if err := m.opts.OnEdit(m.form.id, values); err != nil {
		m.status = errStyle.Render("error: " + err.Error())
		return
	}
	rows := m.tbl.Rows()
	cur := m.tbl.Cursor()
	for _, fld := range m.opts.EditFields {
		if fld.Column >= 0 && fld.Column < len(rows[cur]) {
			rows[cur][fld.Column] = orDashTUI(values[fld.Key])
		}
	}
	m.tbl.SetRows(rows)
	m.status = okStyle.Render("updated " + m.form.id)
	m.form = nil
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
	if m.form != nil {
		return titleStyle.Render(m.opts.Title+" · edit") + "\n" +
			borderStyle.Render(m.formView()) + "\n" +
			m.status + "\n" +
			helpStyle.Render("tab/↑↓ field · ←/→ choice · enter save · esc cancel")
	}
	help := "↑/↓ move · q quit"
	switch {
	case m.opts.OnToggle != nil && m.canEdit():
		help = "↑/↓ move · space enable/disable · e edit · q quit"
	case m.canEdit():
		help = "↑/↓ move · e edit · q quit"
	case m.opts.OnToggle != nil:
		help = "↑/↓ move · space enable/disable · q quit"
	}
	return titleStyle.Render(m.opts.Title) + "\n" +
		borderStyle.Render(m.tbl.View()) + "\n" +
		m.status + "\n" +
		helpStyle.Render(help)
}

func (m tuiModel) formView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n\n", labelStyle.Render("id"), m.form.id)
	for i, fld := range m.opts.EditFields {
		marker := "  "
		if i == m.form.focus {
			marker = focusStyle.Render("> ")
		}
		var val string
		if fld.Kind == FieldChoice {
			val = choiceStyle.Render("< " + m.value(i) + " >")
		} else {
			val = m.form.inputs[i].View()
		}
		fmt.Fprintf(&b, "%s%s%s\n", marker, labelStyle.Render(fld.Label), val)
	}
	return b.String()
}

// indexOf returns the index of v in opts, or -1 if absent.
func indexOf(opts []string, v string) int {
	for i, o := range opts {
		if strings.EqualFold(o, v) {
			return i
		}
	}
	return -1
}

func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// orDashTUI mirrors the CLI's empty-value placeholder so edited cells match
// the rest of the table.
func orDashTUI(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
