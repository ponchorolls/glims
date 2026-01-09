package main

import (
	"database/sql"
	"fmt"
	"strings"
	// "os"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type sessionState int

const (
	stateNav    sessionState = iota // Moving through the list, pressing hotkeys
	stateSearch                     // Typing into the fuzzy finder
	stateAdd                        // Typing into the "Add Item" form
	stateEdit                       // Edit state
	stateAddField
	stateDeleteConfirm // Delete confirmation state
	stateDeleteFieldConfirm
)

type Item struct {
	ID     string
	Name   string
	Values map[string]string // Key: Column Name, Value: Data
}

type model struct {
	table          table.Model
	inputs         []textinput.Model // Dynamic slice of inputs
	fieldNames     []string          // "Name", "Qty", "Location", etc.
	inventory      []Item
	db             *sql.DB
	state          sessionState
	focusIndex     int
	editTargetID   string
	deleteTargetID string
	width          int
	height         int
}

var (
	// Colors
	green = lipgloss.Color("82")
	blue  = lipgloss.Color("33")
	red   = lipgloss.Color("196")
	gray  = lipgloss.Color("240")

	// Styles
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(gray).
			Italic(true)
)

func (m model) Init() tea.Cmd { return textinput.Blink }

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

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetHeight(m.height - 10)
	}
	// 1. Handle Global Keypresses (like Ctrl+C)
	// We check if the message is a KeyMsg first
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		if kmsg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	// 2. Route to Mode-Specific Helpers
	switch m.state {
	case stateSearch:
		return m.handleSearch(msg)
	case stateAdd:
		return m.handleAdd(msg)
	case stateAddField:
		return m.handleAddField(msg)
	case stateEdit:
		return m.handleEdit(msg)
	case stateDeleteFieldConfirm:
		return m.handleDeleteFieldConfirm(msg)
	case stateDeleteConfirm:
		return m.handleDeleteConfirm(msg)
	default: // This handles stateNav
		return m.handleNav(msg)
	}
}

func (m *model) handleNav(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd // Declare cmd here so it can be used below

	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "q":
			return m, tea.Quit
		case "esc":
			m.resetToNav()
			return m, nil
		case "/":
			m.state = stateSearch
			m.inputs[0].Focus()
			m.inputs[0].SetValue("")
			m.inputs[0].Placeholder = "Search names, locations, etc..."
			return m, nil
		case "ctrl+f":
			m.state = stateAddField
			m.inputs[0].SetValue("")
			m.inputs[0].Focus()
			m.inputs[0].Placeholder = "New Column Name"
			return m, nil
		case "ctrl+n":
			m.state = stateAdd
			m.focusIndex = 0
			m.inputs[0].SetValue("")
			m.inputs[0].SetValue("")
			m.inputs[0].Focus()
			return m, nil
		case "ctrl+e":
			currRow := m.table.SelectedRow()
			if len(currRow) > 0 {
				m.state = stateEdit
				m.editTargetID = currRow[0] // Column 0 is always ID

				// Loop through the table row and put values into our inputs
				// Table row structure: [ID, Name, Field1, Field2...]
				for i := 0; i < len(m.inputs); i++ {
					// Table index is i+1 because we skip the ID column
					if i+1 < len(currRow) {
						m.inputs[i].SetValue(currRow[i+1])
					}
				}

				m.focusIndex = 0
				m.inputs[0].Focus()
				return m, nil
			}
		case "ctrl+x":
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

func (m *model) handleAdd(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.resetToNav() // This takes you back to Nav mode
			return m, nil
		case "tab", "up", "down":
			// Cycle through all available inputs
			m.inputs[m.focusIndex].Blur()
			m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
			m.inputs[m.focusIndex].Focus()
			return m, nil

		case "enter":
			// Dynamically build the SQL query
			// We always assume the first input is 'name'
			name := m.inputs[0].Value()

			// Build: INSERT INTO inventory (name, col1, col2) VALUES (?, ?, ?)
			cols := "name"
			placeholders := "?"
			args := []interface{}{name}

			for i := 1; i < len(m.inputs); i++ {
				cols += fmt.Sprintf(", [%s]", m.fieldNames[i])
				placeholders += ", ?"
				args = append(args, m.inputs[i].Value())
			}

			query := fmt.Sprintf("INSERT INTO inventory (%s) VALUES (%s)", cols, placeholders)
			_, _ = m.db.Exec(query, args...)

			m.refreshData() // Helper to reload items and table
			m.resetToNav()
			return m, nil
		}
	}

	// Update the currently focused input
	m.inputs[m.focusIndex], cmd = m.inputs[m.focusIndex].Update(msg)
	return m, cmd
}

func (m *model) handleSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.inputs[0].SetValue("") // Clear the search box
			m.refreshData()          // Reset table to show everything
			m.state = stateNav
			return m, nil
		case "enter":
			m.state = stateNav // Pressing enter can also lock in the current filtered view
			return m, nil
		}
	}

	m.inputs[0], cmd = m.inputs[0].Update(msg)
	searchTerm := m.inputs[0].Value()

	// --- UPDATED TARGET BUILDING ---
	var targets []string
	for _, item := range m.inventory {
		// Start with the name
		searchString := item.Name

		// Append every custom field value so they are searchable too
		for _, val := range item.Values {
			searchString += " " + val
		}

		targets = append(targets, searchString)
	}
	// -------------------------------

	matches := fuzzy.Find(searchTerm, targets)

	// 3. Get fresh headers for the ToRow calls
	_, customCols, _ := GetInventory(m.db)

	var newRows []table.Row
	if searchTerm == "" {
		for _, item := range m.inventory {
			newRows = append(newRows, item.ToRow(customCols))
		}
	} else {
		for _, match := range matches {
			newRows = append(newRows, m.inventory[match.Index].ToRow(customCols))
		}
	}
	m.table.SetRows(newRows)

	return m, cmd
}

func (m *model) handleDeleteConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "y", "Y":
			// PERFORM ACTUAL DELETE
			_, _ = m.db.Exec("DELETE FROM inventory WHERE id = ?", m.deleteTargetID)
			// m.inventory, _ = GetInventory(m.db)
			_, _, err := GetInventory(m.db)
			if err != nil {
				// handle error
			}

			// Refresh table
			var rows []table.Row
			for _, item := range m.inventory {
				_, customCols, _ := GetInventory(m.db) // Get the list of field names
				rows = append(rows, item.ToRow(customCols))
			}
			m.table.SetRows(rows)

			m.refreshData()

			m.state = stateNav
			return m, nil

		case "n", "N", "esc":
			m.state = stateNav // Cancel
			return m, nil
		}
	}
	return m, nil
}

func (m *model) handleEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.resetToNav() // This takes you back to Nav mode
			return m, nil
		case "enter":
			// 1. Start the query with the Name (which is always m.inputs[0])
			query := "UPDATE inventory SET name = ?"
			args := []interface{}{m.inputs[0].Value()}

			// 2. Add every custom field dynamically
			// Remember: m.fieldNames[0] is "Name", m.fieldNames[1] is the first custom field
			for i := 1; i < len(m.inputs); i++ {
				colName := m.fieldNames[i]
				query += fmt.Sprintf(", [%s] = ?", colName)
				args = append(args, m.inputs[i].Value())
			}

			// 3. Add the WHERE clause
			query += " WHERE id = ?"
			args = append(args, m.editTargetID)

			// 4. Execute
			_, err := m.db.Exec(query, args...)
			if err != nil {
				// Log error if needed
			}

			m.refreshData()
			m.resetToNav()
			return m, nil
		case "tab", "down", "j":
			m.inputs[m.focusIndex].Blur()
			m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
			m.inputs[m.focusIndex].Focus()
			return m, nil

		case "shift+tab", "up", "k":
			m.inputs[m.focusIndex].Blur()
			m.focusIndex--
			if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs) - 1
			}
			m.inputs[m.focusIndex].Focus()
			return m, nil
		}
	}

	m.inputs[m.focusIndex], cmd = m.inputs[m.focusIndex].Update(msg)

	return m, cmd
}

func itemsToRows(items []Item, customCols []string) []table.Row {
	var rows []table.Row
	for _, item := range items {
		// item.ToRow also needs to know which columns to pull
		rows = append(rows, item.ToRow(customCols))
	}
	return rows
}

func AddCustomField(db *sql.DB, fieldName string) error {
	// 1. Add to metadata
	_, err := db.Exec("INSERT INTO field_definitions (name) VALUES (?)", fieldName)
	if err != nil {
		return err // Field probably already exists
	}

	// 2. Alter the actual data table
	// SQLite syntax: ALTER TABLE table_name ADD COLUMN column_name TEXT
	query := fmt.Sprintf("ALTER TABLE inventory ADD COLUMN [%s] TEXT DEFAULT ''", fieldName)
	_, err = db.Exec(query)
	return err
}

func (m *model) initDynamicInputs() {
	// Start with the mandatory "Name" field
	m.fieldNames = []string{"Name"}

	// Fetch custom fields from the DB
	rows, _ := m.db.Query("SELECT name FROM field_definitions ORDER BY id ASC")
	for rows.Next() {
		var name string
		rows.Scan(&name)
		m.fieldNames = append(m.fieldNames, name)
	}
	rows.Close()

	// Create a textinput for every field name
	m.inputs = make([]textinput.Model, len(m.fieldNames))
	for i, name := range m.fieldNames {
		t := textinput.New()
		t.Placeholder = name
		t.CharLimit = 64
		t.Width = 30
		// Focus the first one by default
		if i == 0 {
			t.Focus()
		}
		m.inputs[i] = t
	}
}

func (m *model) refreshData() {
	// 1. Get new schema and data first
	items, customCols, err := GetInventory(m.db)
	if err != nil {
		return
	}
	m.inventory = items

	// 2. CRITICAL: Clear existing rows FIRST.
	// This prevents the table from trying to render old data with new headers.
	m.table.SetRows([]table.Row{})

	// 3. Now update the column definitions
	tableCols := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Name", Width: 20},
	}
	for _, col := range customCols {
		tableCols = append(tableCols, table.Column{Title: col, Width: 15})
	}
	m.table.SetColumns(tableCols)

	// 4. Finally, put the new rows in
	m.table.SetRows(itemsToRows(m.inventory, customCols))
}

func (m *model) handleAddField(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc":
			m.resetToNav() // This takes you back to Nav mode
			return m, nil
		case "ctrl+x":
			m.deleteTargetID = m.inputs[0].Value() // Use this field to store the name to delete
			if m.deleteTargetID != "" && m.deleteTargetID != "Name" {
				m.state = stateDeleteFieldConfirm
			}
			return m, nil
		case "enter":
			newField := m.inputs[0].Value()
			if newField != "" {
				AddCustomField(m.db, newField)
				m.initDynamicInputs()
				m.refreshData()
			}
			m.resetToNav()
			return m, nil
		}
	}

	// This line is what allows you to actually type!
	m.inputs[0], cmd = m.inputs[0].Update(msg)
	return m, cmd
}

func (m *model) handleDeleteFieldConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "y", "Y":
			// 1. Physically remove from DB
			_ = DeleteCustomField(m.db, m.deleteTargetID)

			// 2. Update our slice of field names and text inputs
			m.initDynamicInputs()

			// 3. Sync the table (using our new safe refresh)
			m.refreshData()

			m.resetToNav()
			return m, nil

		case "n", "N", "esc":
			m.state = stateAddField
			return m, nil
		}
	}
	return m, nil
}

func (m *model) View() string {
	// 1. Build the Status Bar (Dynamic based on State)
	var statusLine string
	var currentView string

	switch m.state {
	case stateSearch:
		statusLine = lipgloss.NewStyle().Background(blue).Foreground(lipgloss.Color("15")).Bold(true).Render(" SEARCH MODE ")
		currentView = fmt.Sprintf(
			"\n  Search by Name: %s\n\n%s\n\n  (type to filter • esc to clear)",
			m.inputs[0].View(),
			baseStyle.Render(m.table.View()),
		)

	case stateAdd, stateEdit:
		var b strings.Builder
		b.WriteString(fmt.Sprintf("\n  %s\n\n", statusLine))

		for i, field := range m.fieldNames {
			label := field + ":"
			if i == m.focusIndex {
				label = lipgloss.NewStyle().Foreground(green).Bold(true).Render("> " + label)
			}
			b.WriteString(fmt.Sprintf("  %s\n  %s\n\n", label, m.inputs[i].View()))
		}

		b.WriteString("  (enter to save • esc to cancel)")
		currentView = b.String()

	case stateAddField:
		statusLine = lipgloss.NewStyle().Background(blue).Foreground(lipgloss.Color("15")).Render(" FIELD MANAGER ")

		var existing strings.Builder
		existing.WriteString("\n  Current Fields:\n")
		for _, f := range m.fieldNames {
			existing.WriteString(fmt.Sprintf("  • %s\n", f))
		}

		currentView = fmt.Sprintf(
			"%s\n\n  Enter name for NEW column:\n  %s\n\n  (enter to create • type field and [ctrl+x] to delete • esc to cancel)",
			existing.String(),
			m.inputs[0].View(),
		)
	case stateDeleteConfirm:
		statusLine = lipgloss.NewStyle().Background(red).Foreground(lipgloss.Color("15")).Bold(true).Render(" CONFIRM DELETE ")
		currentView = fmt.Sprintf(
			"\n  Are you sure you want to delete item #%s?\n\n  [y] Yes  •  [n] No",
			m.deleteTargetID,
		)

	case stateDeleteFieldConfirm:
		statusLine = lipgloss.NewStyle().Background(red).Foreground(lipgloss.Color("15")).Bold(true).Render(" DELETE COLUMN ")
		currentView = fmt.Sprintf(
			"\n  WARNING: You are about to delete the entire '%s' column.\n  All data stored in this field will be lost forever.\n\n  Confirm deletion? [y] Yes • [n] No",
			m.deleteTargetID,
		)

	default: // stateNav
		statusLine = lipgloss.NewStyle().Background(gray).Foreground(lipgloss.Color("15")).Render(" NAVIGATION ")
		currentView = baseStyle.Render(m.table.View())
	}

	// 2. Assemble the final UI
	header := headerStyle.Render(" GLIMS v1.0 ")

	return fmt.Sprintf(
		"\n%s %s\n\n%s\n\n%s",
		header,
		statusLine,
		currentView,
		footerStyle.Render(" [/] Search • [ctrl+n] New • [ctrl+e] Edit • [ctrl+f] Field Manager • [ctrl+x] Delete • [esc] Reset • [q/ctrl+c] Quit"),
	)
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

func (m *model) resetToNav() {
	m.state = stateNav
	m.focusIndex = 0
	for i := range m.inputs {
		m.inputs[i].Blur()
		m.inputs[i].SetValue("")
	}
}

func main() {
	db, _ := InitDB("glims.db")
	defer db.Close()

	// 1. Fetch data and columns
	items, customCols, _ := GetInventory(db)

	// 2. Build Table Columns dynamically
	tableCols := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Name", Width: 20},
	}
	for _, col := range customCols {
		tableCols = append(tableCols, table.Column{Title: col, Width: 15})
	}

	t := table.New(table.WithColumns(tableCols), table.WithFocused(true), table.WithHeight(10))

	// 3. Populate Rows
	var rows []table.Row
	for _, item := range items {
		rows = append(rows, item.ToRow(customCols))
	}
	t.SetRows(rows)

	m := &model{
		table:     t,
		inventory: items,
		db:        db,
		state:     stateNav,
	}

	// 4. Initialize our dynamic input slice
	m.initDynamicInputs()

	p := tea.NewProgram(m, tea.WithAltScreen())

	// 2. Use 'p' to actually start the application
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
	}
}
