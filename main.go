package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
	_ "modernc.org/sqlite"
)

// --- CONSTANTS & TYPES ---

type sessionState int
type clearStatusMsg struct{}

const (
	stateNav sessionState = iota
	stateSearch
	stateAdd
	stateEdit
	stateAddField
	stateFieldRename
	stateDeleteConfirm
	stateDeleteFieldConfirm
	stateHelp
	stateExportRename
	stateImportPath
)

type Item struct {
	ID     string
	Name   string
	Qty    int
	Values map[string]string
}

type model struct {
	table          table.Model
	inputs         []textinput.Model
	fieldNames     []string
	inventory      []Item
	db             *sql.DB
	state          sessionState
	focusIndex     int
	editTargetID   string
	deleteTargetID string
	width          int
	height         int
	statusMsg      string
	selectedRows   map[string]bool
}

// --- STYLING ---

var (
	background = lipgloss.Color("#282A36")
	foreground = lipgloss.Color("#F8F8F2")
	comment    = lipgloss.Color("#6272A4")
	red        = lipgloss.Color("#FF5555")
	green      = lipgloss.Color("#50FA7B")
	yellow     = lipgloss.Color("#F1FA8C")
	purple     = lipgloss.Color("#BD93F9")
	cyan       = lipgloss.Color("#8BE9FD")
	orange     = lipgloss.Color("#FFB86C")

	headerStyle = lipgloss.NewStyle().Foreground(foreground).Background(background).Padding(0, 1).Bold(true)
	footerStyle = lipgloss.NewStyle().Foreground(comment).Italic(true)
)

// --- DYNAMIC STATE COLORS ---
var (
	colorNav    = purple
	colorSearch = cyan
	colorAdd    = green
	colorEdit   = orange
	colorField  = yellow
	colorDelete = red
)

func (m *model) Init() tea.Cmd { return textinput.Blink }

// --- CORE UPDATE ROUTER ---

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clearStatusMsg:
		m.statusMsg = ""
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.table.SetWidth(m.width - 10)
		m.table.SetHeight(m.height - 18)
	}

	if kmsg, ok := msg.(tea.KeyMsg); ok {
		if kmsg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	switch m.state {
	case stateHelp:
		if kmsg, ok := msg.(tea.KeyMsg); ok && kmsg.String() == "esc" {
			m.state = stateNav
		}
		return m, nil
	case stateAdd, stateEdit:
		return m.handleForm(msg)
	case stateSearch:
		return m.handleSearch(msg)
	case stateAddField, stateFieldRename, stateDeleteFieldConfirm:
		return m.handleAddField(msg)
	case stateExportRename:
		return m.handleExportRename(msg)
	case stateImportPath:
		return m.handleImportPath(msg)
	case stateDeleteConfirm:
		return m.handleDeleteConfirm(msg)
	default:
		return m.handleNav(msg)
	}
}

// --- NAVIGATION HANDLER ---

func (m *model) handleNav(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "q":
			return m, tea.Quit
		case "?":
			m.state = stateHelp
			return m, nil
		case "/":
			m.state = stateSearch
			m.inputs[0].Focus()
			m.inputs[0].SetValue("")
			return m, nil
		case "N":
			m.state = stateAdd
			m.resetInputs()
			m.inputs[0].Focus()
			return m, nil
		case "E":
			currRow := m.table.SelectedRow()
			if len(currRow) > 0 {
				m.state = stateEdit
				m.editTargetID = currRow[0]
				for i := 0; i < len(m.inputs); i++ {
					if i+1 < len(currRow) {
						m.inputs[i].SetValue(currRow[i+1])
					}
				}
				m.focusIndex = 0
				m.inputs[0].Focus()
				return m, nil
			}
		case "F":
			m.state = stateAddField
			m.focusIndex = 0
			return m, nil
		case "P":
			m.state = stateExportRename
			m.inputs[0].SetValue(fmt.Sprintf("inventory_%s.csv", time.Now().Format("2006-01-02")))
			m.inputs[0].Focus()
			return m, nil
		case "I":
			m.state = stateImportPath
			m.inputs[0].Focus()
			m.inputs[0].SetValue("import.csv")
			return m, nil
		case "X":
			m.state = stateDeleteConfirm
			if len(m.getSelectedIDs()) == 0 {
				currRow := m.table.SelectedRow()
				if len(currRow) > 0 {
					m.deleteTargetID = currRow[0]
				}
			}
			return m, nil
		case " ":
			currRow := m.table.SelectedRow()
			if len(currRow) > 0 {
				id := currRow[0]
				m.selectedRows[id] = !m.selectedRows[id]
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// --- VIEW LOGIC ---

func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	var innerView string
	var statusText string
	var borderColor lipgloss.Color

	switch m.state {
	case stateHelp:
		innerView = m.renderHelp()
		statusText = "HELP"
	case stateSearch:
		borderColor = colorSearch
		innerView = "Search: " + m.inputs[0].View() + "\n\n" + m.renderTable()
		statusText = "SEARCH"
	case stateAdd, stateEdit:
		borderColor = colorEdit
		innerView = m.renderForm()
		statusText = "ITEM FORM"
	case stateAddField, stateDeleteFieldConfirm, stateFieldRename:
		borderColor = colorField
		innerView = m.renderFieldManager()
		statusText = "FIELDS"
	case stateExportRename, stateImportPath:
		borderColor = green
		innerView = "Enter Path: " + m.inputs[0].View()
		statusText = "CSV TOOL"
	case stateDeleteConfirm:
		borderColor = colorDelete
		innerView = m.renderDeleteConfirm()
		statusText = "CONFIRM DELETE"
	default:
		borderColor = colorNav
		innerView = m.renderTable()
		statusText = "INVENTORY"
	}

	// Dynamic Style for the Central Box
	contentStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(m.width - 6).
		Height(m.height - 10)

	header := headerStyle.Render(" GLIMS INVENTORY ")
	statusLine := lipgloss.NewStyle().Foreground(borderColor).Bold(true).Render("── " + statusText + " ──")
	footer := footerStyle.Render(" [?] Help • [q] Quit ")

	uiStack := lipgloss.JoinVertical(
		lipgloss.Center,
		header,
		statusLine,
		contentStyle.Render(innerView),
		footer,
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, uiStack)
}

// --- DATABASE & HELPERS ---

func GetInventory(db *sql.DB) ([]Item, []string, error) {
	// 1. Get the custom field names first
	var customCols []string
	fRows, err := db.Query("SELECT name FROM field_definitions ORDER BY id ASC")
	if err == nil {
		for fRows.Next() {
			var colName string
			fRows.Scan(&colName)
			customCols = append(customCols, colName)
		}
		fRows.Close()
	}

	// 2. Get the inventory items
	rows, err := db.Query("SELECT * FROM inventory")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	columnNames, _ := rows.Columns()
	var items []Item

	for rows.Next() {
		// Use a map to handle dynamic columns
		columns := make([]interface{}, len(columnNames))
		columnPointers := make([]interface{}, len(columnNames))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, nil, err
		}

		item := Item{Values: make(map[string]string)}
		for i, colName := range columnNames {
			valStr := fmt.Sprintf("%v", columns[i])
			if columns[i] == nil {
				valStr = ""
			}

			if colName == "id" {
				item.ID = valStr
			} else if colName == "name" {
				item.Name = valStr
			} else {
				item.Values[colName] = valStr
			}
		}
		items = append(items, item)
	}
	return items, customCols, nil
}

func InitDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	// Create inventory table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS inventory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	)`)

	// Create metadata table for custom fields
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS field_definitions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL
	)`)

	return db, err
}

func (m *model) refreshData() {
	items, customCols, _ := GetInventory(m.db)
	m.inventory = items

	cols := []table.Column{{Title: "ID", Width: 4}, {Title: "Name", Width: 30}}
	for _, c := range customCols {
		cols = append(cols, table.Column{Title: c, Width: 15})
	}
	m.table.SetColumns(cols)
	m.table.SetRows(m.itemsToRows(items, customCols))
}

func (m *model) itemsToRows(items []Item, customCols []string) []table.Row {
	var rows []table.Row
	for _, item := range items {
		row := table.Row{item.ID, item.Name}
		for _, c := range customCols {
			row = append(row, item.Values[c])
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *model) renderTable() string {
	s := table.DefaultStyles()
	s.Header = s.Header.BorderBottom(true).Bold(true)
	s.Selected = s.Selected.Background(purple).Foreground(foreground)
	m.table.SetStyles(s)
	return m.table.View()
}

func (m *model) handleForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.state = stateNav
			return m, nil
		case "tab", "down":
			m.inputs[m.focusIndex].Blur()
			m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
			m.inputs[m.focusIndex].Focus()
		case "enter":
			if m.state == stateEdit {
				query := "UPDATE inventory SET name = ?"
				args := []interface{}{m.inputs[0].Value()}
				for i := 1; i < len(m.inputs); i++ {
					query += fmt.Sprintf(", [%s] = ?", m.fieldNames[i])
					args = append(args, m.inputs[i].Value())
				}
				query += " WHERE id = ?"
				args = append(args, m.editTargetID)
				m.db.Exec(query, args...)
			} else {
				cols := "name"
				vals := "?"
				args := []interface{}{m.inputs[0].Value()}
				for i := 1; i < len(m.inputs); i++ {
					cols += fmt.Sprintf(", [%s]", m.fieldNames[i])
					vals += ", ?"
					args = append(args, m.inputs[i].Value())
				}
				m.db.Exec(fmt.Sprintf("INSERT INTO inventory (%s) VALUES (%s)", cols, vals), args...)
			}
			m.refreshData()
			m.state = stateNav
			return m, nil
		}
	}
	m.inputs[m.focusIndex], cmd = m.inputs[m.focusIndex].Update(msg)
	return m, cmd
}

func (m *model) handleSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		if kmsg.String() == "esc" || kmsg.String() == "enter" {
			m.state = stateNav
			m.refreshData()
			return m, nil
		}
	}
	m.inputs[0], cmd = m.inputs[0].Update(msg)
	searchTerm := m.inputs[0].Value()
	var targets []string
	for _, item := range m.inventory {
		searchStr := item.Name
		for _, v := range item.Values {
			searchStr += " " + v
		}
		targets = append(targets, searchStr)
	}
	matches := fuzzy.Find(searchTerm, targets)
	_, customCols, _ := GetInventory(m.db)
	var newRows []table.Row
	for _, match := range matches {
		newRows = append(newRows, m.inventory[match.Index].ToRow(customCols))
	}
	m.table.SetRows(newRows)
	return m, cmd
}

func AddCustomField(db *sql.DB, fieldName string) error {
	// 1. Add to metadata table
	_, err := db.Exec("INSERT INTO field_definitions (name) VALUES (?)", fieldName)
	if err != nil {
		return err // Likely the field already exists
	}

	// 2. Alter the actual data table to include the new column
	// We use brackets [] to handle field names with spaces or reserved words
	query := fmt.Sprintf("ALTER TABLE inventory ADD COLUMN [%s] TEXT DEFAULT ''", fieldName)
	_, err = db.Exec(query)
	return err
}

func DeleteCustomField(db *sql.DB, fieldName string) error {
	_, err := db.Exec("DELETE FROM field_definitions WHERE name = ?", fieldName)
	if err != nil {
		return err
	}
	// SQLite doesn't support dropping columns easily in old versions,
	// but modern versions (3.35.0+) support:
	query := fmt.Sprintf("ALTER TABLE inventory DROP COLUMN [%s]", fieldName)
	_, err = db.Exec(query)
	return err
}

func (m *model) handleAddField(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// --- BRANCH 1: User is TYPING (Rename/Add) ---
	if m.state == stateFieldRename {
		if kmsg, ok := msg.(tea.KeyMsg); ok {
			switch kmsg.String() {
			case "esc":
				m.state = stateAddField
				m.inputs[0].Blur()
				return m, nil
			case "enter":
				newName := m.inputs[0].Value()
				if newName != "" {
					if m.focusIndex == -1 {
						AddCustomField(m.db, newName)
					} else {
						oldName := m.fieldNames[m.focusIndex]
						m.renameField(oldName, newName)
					}
					m.initDynamicInputs()
					m.refreshData()
				}
				m.state = stateAddField // Stay in manager
				m.inputs[0].Blur()
				return m, nil
			}
		}
		m.inputs[0], cmd = m.inputs[0].Update(msg)
		return m, cmd
	}

	// --- BRANCH 2: DELETE CONFIRMATION ---
	if m.state == stateDeleteFieldConfirm {
		if kmsg, ok := msg.(tea.KeyMsg); ok {
			switch kmsg.String() {
			case "y", "Y":
				DeleteCustomField(m.db, m.deleteTargetID)
				m.initDynamicInputs()
				m.refreshData()
				m.state = stateAddField // Stay in manager
				return m, nil
			case "n", "N", "esc":
				m.state = stateAddField // Stay in manager
				return m, nil
			}
		}
		return m, nil
	}

	// --- BRANCH 3: LIST BROWSING ---
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "up", "k":
			if m.focusIndex > 0 {
				m.focusIndex--
			}
			return m, nil
		case "down", "j":
			if m.focusIndex < len(m.fieldNames)-1 {
				m.focusIndex++
			}
			return m, nil
		case "R":
			m.state = stateFieldRename
			m.inputs[0].SetValue(m.fieldNames[m.focusIndex])
			m.inputs[0].Focus()       // <--- Ensure this is here
			return m, textinput.Blink // <--- Return the blink command
		case "A":
			m.state = stateFieldRename
			m.focusIndex = -1
			m.inputs[0].SetValue("")
			m.inputs[0].Focus() // <--- Ensure this is here
			return m, textinput.Blink
		case "X":
			m.deleteTargetID = m.fieldNames[m.focusIndex]
			m.state = stateDeleteFieldConfirm
			return m, nil
		case "esc":
			m.resetToNav() // ONLY esc takes you out
			return m, nil
		}
	}
	return m, nil
}

func (m *model) handleExportRename(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.resetToNav()
			return m, nil
		case "enter":
			filename := m.inputs[0].Value()
			if !strings.HasSuffix(filename, ".csv") {
				filename += ".csv"
			}
			err := m.exportToCSV(filename)
			if err != nil {
				m.statusMsg = "Export failed!"
			} else {
				m.statusMsg = "Exported to " + filename
			}
			m.resetToNav()
			return m, nil
		}
	}
	m.inputs[0], cmd = m.inputs[0].Update(msg)
	return m, cmd
}

func (m *model) handleImportPath(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.resetToNav()
			return m, nil
		case "enter":
			count, err := m.importFromCSV(m.inputs[0].Value())
			if err != nil {
				m.statusMsg = fmt.Sprintf("Error: %v", err)
			} else {
				m.statusMsg = fmt.Sprintf("Successfully imported %d items!", count)
			}
			m.initDynamicInputs() // Refresh field list in case new ones were added
			m.refreshData()
			m.state = stateNav
			return m, nil
		}
	}
	m.inputs[0], cmd = m.inputs[0].Update(msg)
	return m, cmd
}

func (m *model) handleDeleteConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "y", "Y":
			var idsToDelete []string

			// 1. Collect IDs: Prioritize the green selections
			for id, selected := range m.selectedRows {
				if selected {
					idsToDelete = append(idsToDelete, id)
				}
			}

			// 2. Fallback: If no green items, use the single target
			if len(idsToDelete) == 0 && m.deleteTargetID != "" {
				idsToDelete = append(idsToDelete, m.deleteTargetID)
			}

			// 3. Execution
			if len(idsToDelete) > 0 {
				for _, id := range idsToDelete {
					m.db.Exec("DELETE FROM inventory WHERE id = ?", id)
				}

				count := len(idsToDelete)
				m.selectedRows = make(map[string]bool) // Clear selections
				m.deleteTargetID = ""                  // Clear single target
				m.refreshData()                        // Rebuild table rows

				m.state = stateNav
				return m, m.setStatus(fmt.Sprintf("Successfully deleted %d item(s)", count))
			}

			m.state = stateNav
			return m, nil

		case "n", "N", "esc":
			m.state = stateNav
			m.deleteTargetID = ""
			return m, nil
		}
	}
	return m, nil
}

func (m *model) resetInputs() {
	for i := range m.inputs {
		m.inputs[i].SetValue("")
		m.inputs[i].Blur()
	}
	m.focusIndex = 0
}

func (m *model) renderHelp() string {
	return `
    KEYBOARD SHORTCUTS
    ------------------
    [/]      Search Items
    [N] Add New Item
    [E] Edit Selected Item
    [X] Delete Selected Item

    [F] Field Manager
    [I] Import CSV from 'import.csv'
    [P] Export to CSV

    [?]      Toggle Help
    [esc]    Back / Reset
    [q]      Quit
    `
}

func (m *model) renderDeleteConfirm() string {
	ids := m.getSelectedIDs()
	msg := fmt.Sprintf("Delete item #%s?", m.deleteTargetID)
	if len(ids) > 0 {
		msg = fmt.Sprintf("Delete %d selected items?", len(ids))
	}
	return fmt.Sprintf("\n\n  %s\n\n  [y] Confirm  •  [n] Cancel",
		lipgloss.NewStyle().Foreground(red).Bold(true).Render(msg))
}

func (m *model) getSelectedIDs() []string {
	var ids []string
	for id, selected := range m.selectedRows {
		if selected {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *model) resetToNav() {
	m.state = stateNav
	for i := range m.inputs {
		m.inputs[i].Blur()
		m.inputs[i].SetValue("")
	}
	m.refreshData() // This ensures the table shows everything again
}

func (m *model) renderForm() string {
	var b strings.Builder
	for i, field := range m.fieldNames {
		label := field + ":"
		if i == m.focusIndex {
			label = lipgloss.NewStyle().Foreground(green).Bold(true).Render("> " + label)
		}
		b.WriteString(fmt.Sprintf("  %s\n  %s\n\n", label, m.inputs[i].View()))
	}
	b.WriteString("\n  [tab] cycle • [enter] save • [ctrl+s] save & another • [esc] cancel")
	return b.String()
}

func (m *model) renderFieldManager() string {
	var b strings.Builder

	// 1. Header Logic
	headerStyle := lipgloss.NewStyle().Foreground(purple).Bold(true).Underline(true)
	b.WriteString(headerStyle.Render(" FIELD MANAGEMENT ") + "\n\n")
	// Inside renderFieldManager, before the loop
	b.WriteString("  ") // Just a little nudge to the right
	// 2. The List (Only show list if we aren't in a deep confirmation state)
	if m.state != stateDeleteFieldConfirm {
		for i, f := range m.fieldNames {
			cursor := "  "
			style := lipgloss.NewStyle()

			if i == m.focusIndex {
				cursor = "> "
				style = style.Foreground(purple).Bold(true)
			}

			// We use %-20s to keep the layout stable
			line := fmt.Sprintf("%s %-20s", cursor, f)
			b.WriteString(style.Render(line) + "\n")
		}
		b.WriteString("\n" + strings.Repeat("─", 30) + "\n")
	}

	// 3. Dynamic Footer/Input Logic
	switch m.state {
	case stateFieldRename:
		// Check if we are adding new or renaming existing
		title := " Rename Field: "
		if m.inputs[0].Value() == "" && m.focusIndex == -1 {
			title = " Add New Field: "
		}

		b.WriteString(lipgloss.NewStyle().Foreground(yellow).Render(title) + "\n")
		b.WriteString(m.inputs[0].View() + "\n\n")
		b.WriteString(footerStyle.Render(" (enter to save • esc to cancel)"))

	case stateDeleteFieldConfirm:
		// Big warning for deletion
		warnStyle := lipgloss.NewStyle().Foreground(red).Bold(true)
		b.WriteString(warnStyle.Render(" ! CAUTION ! ") + "\n\n")
		b.WriteString(fmt.Sprintf(" Delete the column '%s'?\n", m.deleteTargetID))
		b.WriteString(" This will remove all data in this column.\n\n")
		b.WriteString(footerStyle.Render(" [y] Confirm Delete • [n/esc] Cancel"))

	default:
		// Navigation mode within Field Manager
		b.WriteString(footerStyle.Render(" [R] Rename  [A] Add  [X] Delete  [esc] Exit"))
	}

	return b.String()
}
func (m *model) exportToCSV(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()

	headers := []string{"ID", "Name"}
	headers = append(headers, m.fieldNames[1:]...)
	writer.Write(headers)

	for _, item := range m.inventory {
		row := []string{item.ID, item.Name}
		for _, field := range m.fieldNames[1:] {
			row = append(row, item.Values[field])
		}
		writer.Write(row)
	}
	return nil
}

func (m *model) importFromCSV(filename string) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, err
	}

	if len(records) < 2 {
		return 0, fmt.Errorf("CSV file is empty")
	}

	headers := records[0]
	// 1. Ensure all columns in CSV exist in DB
	for _, header := range headers {
		if strings.ToLower(header) == "id" || strings.ToLower(header) == "name" {
			continue
		}
		// AddCustomField ignores if already exists due to UNIQUE constraint
		AddCustomField(m.db, header)
	}

	// 2. Insert records
	count := 0
	for _, record := range records[1:] {
		cols := "name"
		placeholders := "?"
		var nameVal string
		var args []interface{}

		// Map record values to headers
		valMap := make(map[string]string)
		for i, val := range record {
			if i < len(headers) {
				h := headers[i]
				if strings.ToLower(h) == "name" {
					nameVal = val
				} else if strings.ToLower(h) != "id" {
					valMap[h] = val
				}
			}
		}

		args = append(args, nameVal)
		for k, v := range valMap {
			cols += fmt.Sprintf(", [%s]", k)
			placeholders += ", ?"
			args = append(args, v)
		}

		query := fmt.Sprintf("INSERT INTO inventory (%s) VALUES (%s)", cols, placeholders)
		_, err := m.db.Exec(query, args...)
		if err == nil {
			count++
		}
	}
	return count, nil
}

func (m *model) initDynamicInputs() {
	m.fieldNames = []string{"Name"}
	rows, _ := m.db.Query("SELECT name FROM field_definitions ORDER BY id ASC")
	for rows.Next() {
		var name string
		rows.Scan(&name)
		m.fieldNames = append(m.fieldNames, name)
	}
	rows.Close()

	m.inputs = make([]textinput.Model, len(m.fieldNames))
	for i, name := range m.fieldNames {
		t := textinput.New()
		t.Placeholder = name
		// Ensure the first input is ready to receive text
		if i == 0 {
			t.Focus()
		}
		m.inputs[i] = t
	}
}
func (i Item) ToRow(customCols []string) table.Row {
	row := table.Row{i.ID, i.Name}
	for _, colName := range customCols {
		val, exists := i.Values[colName]
		if !exists {
			val = ""
		}
		row = append(row, val)
	}
	return row
}

func (m *model) renameField(oldName, newName string) {
	tx, err := m.db.Begin()
	if err != nil {
		m.statusMsg = "Transaction error"
		return
	}

	// 1. Rename column in data table
	query := fmt.Sprintf("ALTER TABLE inventory RENAME COLUMN [%s] TO [%s]", oldName, newName)
	if _, err := tx.Exec(query); err != nil {
		tx.Rollback()
		m.statusMsg = "Rename failed: " + err.Error()
		return
	}

	// 2. Update metadata
	if _, err := tx.Exec("UPDATE field_definitions SET name = ? WHERE name = ?", newName, oldName); err != nil {
		tx.Rollback()
		m.statusMsg = "Metadata update failed"
		return
	}

	tx.Commit()
	m.statusMsg = fmt.Sprintf("Success: %s -> %s", oldName, newName)
}

func (m *model) setStatus(msg string) tea.Cmd {
	m.statusMsg = msg
	return tea.Tick(time.Second*3, func(t time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

// --- MAIN ---

func main() {
	db, err := InitDB("glims.db")
	if err != nil {
		fmt.Printf("DB Error: %v", err)
		return
	}
	defer db.Close()

	// 1. Initial Data Load
	items, customCols, _ := GetInventory(db)

	// 2. Setup Table
	tableCols := []table.Column{{Title: "ID", Width: 4}, {Title: "Name", Width: 30}}
	for _, col := range customCols {
		tableCols = append(tableCols, table.Column{Title: col, Width: 15})
	}

	t := table.New(table.WithColumns(tableCols), table.WithFocused(true), table.WithHeight(10))

	// 3. Setup Model
	m := &model{
		db:           db,
		table:        t,
		inventory:    items,
		selectedRows: make(map[string]bool),
		state:        stateNav,
	}

	// 4. Initialize dynamic components
	m.initDynamicInputs()
	m.refreshData()

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
	}
}
