package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
	_ "modernc.org/sqlite" // Pure Go driver
	"os"
	"strings"
	"time"
)

type sessionState int

const (
	stateNav sessionState = iota
	stateSearch
	stateAdd
	stateEdit
	stateAddField
	stateDeleteConfirm
	stateDeleteFieldConfirm
	stateHelp
	stateExportRename sessionState = iota
	stateImportPath   sessionState = iota
	stateImportResult
)

type Item struct {
	ID     string
	Name   string
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
}

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

// Dynamic State Colors
var (
	colorNav    = purple
	colorSearch = cyan
	colorAdd    = green
	colorEdit   = orange
	colorField  = yellow
	colorDelete = red
)

func (m model) Init() tea.Cmd { return textinput.Blink }

// --- DATABASE HELPERS ---

func GetColumns(db *sql.DB) []table.Column {
	cols := []table.Column{{Title: "ID", Width: 4}, {Title: "Name", Width: 20}}
	rows, _ := db.Query("SELECT name FROM field_definitions")
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		cols = append(cols, table.Column{Title: name, Width: 15})
	}
	return cols
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

// --- CSV LOGIC ---

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

// --- UPDATE LOGIC ---

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetHeight(m.height - 18)
	}

	if kmsg, ok := msg.(tea.KeyMsg); ok {
		if kmsg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	switch m.state {
	case stateHelp:
		if kmsg, ok := msg.(tea.KeyMsg); ok {
			if kmsg.String() == "esc" || kmsg.String() == "?" || kmsg.String() == "q" {
				m.state = stateNav
			}
		}
		return m, nil
	case stateSearch:
		return m.handleSearch(msg)
	case stateAdd:
		return m.handleAdd(msg)
	case stateAddField:
		return m.handleAddField(msg)
	case stateEdit:
		return m.handleEdit(msg)
	case stateImportPath:
		return m.handleImportPath(msg)
	case stateExportRename:
		return m.handleExportRename(msg)
	case stateDeleteFieldConfirm:
		return m.handleDeleteFieldConfirm(msg)
	case stateDeleteConfirm:
		return m.handleDeleteConfirm(msg)
	default:
		return m.handleNav(msg)
	}
}

func (m *model) handleNav(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "q":
			return m, tea.Quit
		case "esc":
			m.resetToNav()
			return m, nil
		case "?":
			m.state = stateHelp
			return m, nil
		case "/":
			m.state = stateSearch
			m.inputs[0].Focus()
			m.inputs[0].SetValue("")
			return m, nil
		case "I":
			m.state = stateImportPath
			m.inputs[0].SetValue("import.csv")
			m.inputs[0].Focus()
			return m, nil
		case "P":
			m.state = stateExportRename
			defaultName := fmt.Sprintf("inventory_%s.csv", time.Now().Format("2006-01-02_15-04"))
			m.inputs[0].SetValue(defaultName)
			m.inputs[0].Focus()
			return m, nil
		case "F":
			m.state = stateAddField
			m.inputs[0].Focus()
			return m, nil
		case "N":
			m.state = stateAdd
			m.focusIndex = 0
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
		case "X":
			currRow := m.table.SelectedRow()
			if len(currRow) > 0 {
				m.deleteTargetID = currRow[0]
				m.state = stateDeleteConfirm
				return m, nil
			}
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// --- VIEW LOGIC ---

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	var borderColor lipgloss.Color
	var statusText string

	switch m.state {
	case stateSearch:
		borderColor = colorSearch
		statusText = "SEARCH MODE"
	case stateAdd:
		borderColor = colorAdd
		statusText = "ADD ITEM"
	case stateEdit:
		borderColor = colorEdit
		statusText = "EDIT ITEM"
	case stateExportRename:
		borderColor = green
		statusText = "EXPORT CSV"
	case stateAddField, stateDeleteFieldConfirm:
		borderColor = colorField
		statusText = "FIELD MANAGER"
	case stateDeleteConfirm:
		borderColor = colorDelete
		statusText = "DELETE ITEM"
	default:
		borderColor = colorNav
		statusText = "NAVIGATION"
	}

	// Dynamic Style for the Central Box
	contentStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(m.width - 6).
		Height(m.height - 10)

	var innerView string
	switch m.state {
	case stateHelp:
		innerView = m.renderHelp()
	case stateAdd, stateEdit:
		innerView = m.renderForm()
	case stateAddField, stateDeleteFieldConfirm:
		innerView = m.renderFieldManager()
	case stateSearch:
		innerView = lipgloss.JoinVertical(lipgloss.Left,
			"Search: "+m.inputs[0].View(),
			"",
			m.table.View(),
		)
	case stateImportPath:
		innerView = fmt.Sprintf(
			"\n  Import CSV File\n  Path: %s\n\n  (enter to start • esc to cancel)",
			m.inputs[0].View(),
		)
	case stateExportRename:
		innerView = fmt.Sprintf(
			"\n  Enter Filename:\n\n  %s\n\n  (enter to save • esc to cancel)",
			m.inputs[0].View(),
		)
	case stateDeleteConfirm:
		innerView = fmt.Sprintf("\n\n  Are you sure you want to delete item #%s?\n\n  [y] Yes  •  [n] No", m.deleteTargetID)
	default:
		innerView = m.table.View()
	}

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

func (m *model) renderForm() string {
	var b strings.Builder
	for i, field := range m.fieldNames {
		label := field + ":"
		if i == m.focusIndex {
			label = lipgloss.NewStyle().Foreground(green).Bold(true).Render("> " + label)
		}
		b.WriteString(fmt.Sprintf("  %s\n  %s\n\n", label, m.inputs[i].View()))
	}
	b.WriteString("\n  (tab to cycle • enter to save • esc to cancel)")
	return b.String()
}

func (m *model) renderFieldManager() string {
	if m.state == stateDeleteFieldConfirm {
		return fmt.Sprintf("\n\n  WARNING: Delete column '%s'?\n\n  [y] Confirm  •  [n] Cancel", m.deleteTargetID)
	}
	var b strings.Builder
	b.WriteString("Current Columns:\n")
	for _, f := range m.fieldNames {
		b.WriteString(fmt.Sprintf(" • %s\n", f))
	}
	b.WriteString("\nNew Column Name:\n" + m.inputs[0].View())
	b.WriteString("\n\n(enter to create • type name and [ctrl+x] to delete)")
	return b.String()
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

// --- SUPPORTING FUNCTIONS (STUBS/FIXES) ---

func (m *model) resetToNav() {
	m.state = stateNav
	m.statusMsg = ""
	for i := range m.inputs {
		m.inputs[i].Blur()
		m.inputs[i].SetValue("")
	}
}

func (m *model) handleAdd(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.resetToNav()
			return m, nil
		case "tab", "down":
			m.inputs[m.focusIndex].Blur()
			m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
			m.inputs[m.focusIndex].Focus()
		case "enter":
			cols := "name"
			placeholders := "?"
			args := []interface{}{m.inputs[0].Value()}
			for i := 1; i < len(m.inputs); i++ {
				cols += fmt.Sprintf(", [%s]", m.fieldNames[i])
				placeholders += ", ?"
				args = append(args, m.inputs[i].Value())
			}
			query := fmt.Sprintf("INSERT INTO inventory (%s) VALUES (%s)", cols, placeholders)
			m.db.Exec(query, args...)
			m.refreshData()
			m.resetToNav()
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

func (m *model) handleEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.resetToNav()
			return m, nil
		case "tab", "down":
			m.inputs[m.focusIndex].Blur()
			m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
			m.inputs[m.focusIndex].Focus()
		case "enter":
			query := "UPDATE inventory SET name = ?"
			args := []interface{}{m.inputs[0].Value()}
			for i := 1; i < len(m.inputs); i++ {
				query += fmt.Sprintf(", [%s] = ?", m.fieldNames[i])
				args = append(args, m.inputs[i].Value())
			}
			query += " WHERE id = ?"
			args = append(args, m.editTargetID)
			m.db.Exec(query, args...)
			m.refreshData()
			m.resetToNav()
			return m, nil
		}
	}
	m.inputs[m.focusIndex], cmd = m.inputs[m.focusIndex].Update(msg)
	return m, cmd
}

func (m *model) handleDeleteConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "y", "Y":
			m.db.Exec("DELETE FROM inventory WHERE id = ?", m.deleteTargetID)
			m.refreshData()
			m.state = stateNav
		case "n", "N", "esc":
			m.state = stateNav
		}
	}
	return m, nil
}

func (m *model) handleAddField(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.resetToNav()
			return m, nil
		case "X":
			m.deleteTargetID = m.inputs[0].Value()
			if m.deleteTargetID != "" && m.deleteTargetID != "Name" {
				m.state = stateDeleteFieldConfirm
			}
		case "enter":
			newF := m.inputs[0].Value()
			if newF != "" {
				AddCustomField(m.db, newF)
				m.initDynamicInputs()
				m.refreshData()
			}
			m.resetToNav()
		}
	}
	m.inputs[0], cmd = m.inputs[0].Update(msg)
	return m, cmd
}

func (m *model) handleDeleteFieldConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "y", "Y":
			DeleteCustomField(m.db, m.deleteTargetID)
			m.initDynamicInputs()
			m.refreshData()
			m.resetToNav()
		case "n", "N", "esc":
			m.state = stateAddField
		}
	}
	return m, nil
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
		m.inputs[i] = t
	}
}

func (m *model) refreshData() {
	items, customCols, _ := GetInventory(m.db)
	m.inventory = items
	m.table.SetRows([]table.Row{})
	cols := []table.Column{{Title: "ID", Width: 4}, {Title: "Name", Width: 20}}
	for _, c := range customCols {
		cols = append(cols, table.Column{Title: c, Width: 15})
	}
	m.table.SetColumns(cols)
	m.table.SetRows(itemsToRows(m.inventory, customCols))
}

func main() {
	db, _ := InitDB("glims.db")
	defer db.Close()
	items, customCols, _ := GetInventory(db)
	tableCols := []table.Column{{Title: "ID", Width: 4}, {Title: "Name", Width: 20}}
	for _, col := range customCols {
		tableCols = append(tableCols, table.Column{Title: col, Width: 15})
	}
	t := table.New(table.WithColumns(tableCols), table.WithFocused(true), table.WithHeight(10))
	t.SetRows(itemsToRows(items, customCols))

	m := &model{
		table:     t,
		inventory: items,
		db:        db,
		state:     stateNav,
	}
	m.initDynamicInputs()
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
	}
}

// itemsToRows converts our slice of Item structs into the format the table component understands
func itemsToRows(items []Item, customCols []string) []table.Row {
	var rows []table.Row
	for _, item := range items {
		rows = append(rows, item.ToRow(customCols))
	}
	return rows
}

// AddCustomField adds a new column to the database and tracks it in field_definitions
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

// GetInventory fetches all data and returns the items and the list of custom column names
func GetInventory(db *sql.DB) ([]Item, []string, error) {
	// 1. Get custom field names first
	var customCols []string
	fRows, err := db.Query("SELECT name FROM field_definitions ORDER BY id ASC")
	if err == nil {
		for fRows.Next() {
			var name string
			fRows.Scan(&name)
			customCols = append(customCols, name)
		}
		fRows.Close()
	}

	// 2. Get all items
	rows, err := db.Query("SELECT * FROM inventory")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	// Use column names from the query to map data correctly
	cols, _ := rows.Columns()
	var items []Item

	for rows.Next() {
		// Create a slice of interfaces to hold row data
		columns := make([]interface{}, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, nil, err
		}

		item := Item{Values: make(map[string]string)}
		for i, colName := range cols {
			val := ""
			if columns[i] != nil {
				val = fmt.Sprintf("%v", columns[i])
			}

			if colName == "id" {
				item.ID = val
			} else if colName == "name" {
				item.Name = val
			} else {
				item.Values[colName] = val
			}
		}
		items = append(items, item)
	}

	return items, customCols, nil
}

// InitDB sets up the SQLite tables if they don't exist
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
