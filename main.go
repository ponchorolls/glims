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
	Qty    string
	Values map[string]string
}

type model struct {
	table             table.Model
	inputs            []textinput.Model
	fieldNames        []string
	inventory         []Item
	db                *sql.DB
	state             sessionState
	focusIndex        int
	editTargetID      string
	deleteTargetID    string
	width             int
	height            int
	statusMsg         string
	selectedRows      map[string]bool
	lastSelectedIndex int
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

var (
	qtyCritical = lipgloss.NewStyle().Foreground(red).Bold(true) // 0 items
	qtyLow      = lipgloss.NewStyle().Foreground(yellow)         // 1-5 items
	qtyNormal   = lipgloss.NewStyle().Foreground(green)          // 6+ items
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
		m.refreshData()
		return m, nil
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
		currRow := m.table.SelectedRow()

		switch kmsg.String() {
		case "q":
			return m, tea.Quit

		case "esc":
			m.resetToNav()
			return m, nil

		case "?":
			m.state = stateHelp
			return m, nil

		case "s": // Regular Space: Toggle and set Anchor
			currRow := m.table.SelectedRow()
			if len(currRow) > 0 {
				id := currRow[0]
				m.selectedRows[id] = !m.selectedRows[id]
				m.lastSelectedIndex = m.table.Cursor() // Set the anchor
				m.refreshData()
			}
			return m, nil

		case "shift+s", "S": // Shift+Space: Select Range
			currRow := m.table.SelectedRow()
			if len(currRow) > 0 {
				m.selectRange(m.table.Cursor())
				m.refreshData()
			}
			return m, nil
		case "/":
			m.state = stateSearch
			m.inputs[0].SetValue("")
			m.inputs[0].PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#44475A"))
			m.inputs[0].Placeholder = "type to filter..."
			m.inputs[0].Focus()
			// Disable prompt symbols so it looks clean in the bar
			m.inputs[0].Prompt = ""
			return m, nil
		case "n", "N": // Add New
			m.state = stateAdd
			m.resetInputs()
			m.inputs[0].Focus()
			return m, nil

		case "e", "E": // Edit
			if len(currRow) > 0 {
				m.state = stateEdit
				m.editTargetID = currRow[0]
				for i := 0; i < len(m.inputs); i++ {
					if i+1 < len(currRow) {
						val := currRow[i+1]
						if i == 0 { // Strip symbols from Name
							val = strings.TrimPrefix(val, "○ ")
							val = strings.TrimPrefix(val, "● ")
						}
						m.inputs[i].SetValue(val)
					}
				}
				m.focusIndex = 0
				m.inputs[0].Focus()
				return m, nil
			}

		case "f", "F": // Field Manager
			m.state = stateAddField
			m.focusIndex = 0
			m.inputs[0] = textinput.New()
			return m, nil

		case "x", "X": // Delete
			if len(m.getSelectedIDs()) > 0 || len(currRow) > 0 {
				m.state = stateDeleteConfirm
				if len(m.getSelectedIDs()) == 0 {
					m.deleteTargetID = currRow[0]
				}
			}
			return m, nil

		case "p", "P": // Export
			m.state = stateExportRename
			m.inputs[0] = textinput.New()
			m.inputs[0].Focus()
			m.inputs[0].SetValue(fmt.Sprintf("inventory_%s.csv", time.Now().Format("2006-01-02")))
			return m, nil

		case "i", "I": // Import
			m.state = stateImportPath
			m.inputs[0] = textinput.New()
			m.inputs[0].Focus()
			m.inputs[0].Placeholder = "path/to/file.csv"
			m.inputs[0].SetValue("import.csv")
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

	// 1. Header
	header := lipgloss.NewStyle().Foreground(purple).Bold(true).Margin(1, 0, 1, 2).Render("GLIMS - Everything you own at a GLIMS") + lipgloss.NewStyle().Foreground(comment).Render(fmt.Sprintf(" v0.1.0 [%s]", strings.ToUpper(m.getStatusText())))

	// 2. Main Content
	var content string
	switch m.state {
	case stateAdd, stateEdit:
		content = m.renderForm()
	case stateHelp:
		content = m.renderHelp()
	case stateDeleteConfirm:
		content = m.renderDeleteConfirm()
	case stateAddField, stateFieldRename, stateDeleteFieldConfirm:
		content = m.renderFieldManager()
	case stateExportRename, stateImportPath:
		label := " FILE PATH: "
		content = lipgloss.NewStyle().Background(yellow).Foreground(background).Render(label) + " " + m.inputs[0].View()
	default:
		// Search results are rendered directly into the table in handleSearch
		content = m.renderTable()
	}

	styledContent := lipgloss.NewStyle().Margin(0, 2).Render(content)

	var bar string
	barStyle := lipgloss.NewStyle().Foreground(background).Padding(0, 1).Margin(0, 2).Bold(true)

	if m.state == stateSearch {
		// ... (Keep your Yellow Search Bar logic here)
		matchCount := len(m.table.Rows())
		searchText := " SEARCH: " + m.inputs[0].View()
		countText := fmt.Sprintf(" MATCHES: %d ", matchCount)
		spaceCount := m.width - lipgloss.Width(searchText) - lipgloss.Width(countText) - 6
		if spaceCount < 0 {
			spaceCount = 1
		}
		gap := strings.Repeat(" ", spaceCount)
		bar = barStyle.Background(yellow).Render(searchText + gap + countText)

	} else if m.state == stateNav {
		selectedCount := 0
		for _, isSelected := range m.selectedRows {
			if isSelected {
				selectedCount++
			}
		}

		// 1. Get the cursor index from the table
		cursor := m.table.Cursor()

		// 2. Safety check: make sure the cursor is within bounds of our inventory
		if cursor >= 0 && cursor < len(m.inventory) {
			item := m.inventory[cursor]

			// 3. Get current Qty
			var qVal int
			fmt.Sscanf(item.Qty, "%d", &qVal)

			// 4. Set Default Thresholds
			highTarget := 30
			lowTarget := 10

			// 5. Scan the Item's Values map directly (much safer than row indexes!)
			if val, ok := item.Values["green"]; ok && strings.TrimSpace(val) != "" {
				fmt.Sscanf(val, "%d", &highTarget)
			}
			if val, ok := item.Values["orange"]; ok && strings.TrimSpace(val) != "" {
				fmt.Sscanf(val, "%d", &lowTarget)
			}

			// 6. Logic for background color
			statusColor := red
			if qVal >= highTarget {
				statusColor = green
			} else if qVal >= lowTarget {
				statusColor = orange
			}

			var info string
			if selectedCount > 1 {
				info = fmt.Sprintf(" SELECTED: %d ITEMS | CURRENT: %s ", selectedCount, item.Name)
			} else {
				info = fmt.Sprintf(" ITEM: %s | QTY: %s | THRESHOLDS: G:%d O:%d ", item.Name, item.Qty, highTarget, lowTarget)
			}
			bar = barStyle.Background(statusColor).Render(info)
		}
	}

	// 4. Footer & Padding
	footer := m.renderFooter()
	bodyHeight := lipgloss.Height(header) + lipgloss.Height(styledContent) + lipgloss.Height(bar) + lipgloss.Height(footer)

	padding := ""
	if m.height > bodyHeight {
		padding = strings.Repeat("\n", m.height-bodyHeight)
	}

	return header + "\n" + styledContent + padding + bar + "\n" + footer
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
			} else if colName == "qty" {
				item.Qty = valStr // Map the DB qty to the Item struct
			} else {
				item.Values[colName] = valStr
			}
		}
		items = append(items, item)
	}
	return items, customCols, nil
}

func (m *model) selectRange(currentIndex int) {
	start := m.lastSelectedIndex
	end := currentIndex

	// Ensure start is the smaller number for the loop
	if start > end {
		start, end = end, start
	}

	// Get the state of the anchor row to apply it to the range
	// (e.g., if you select the anchor, Shift+Space selects the whole range)
	rows := m.table.Rows()
	if start < 0 || end >= len(rows) {
		return
	}

	anchorID := rows[m.lastSelectedIndex][0]
	targetState := m.selectedRows[anchorID]

	for i := start; i <= end; i++ {
		id := rows[i][0]
		m.selectedRows[id] = targetState
	}
}

func (m *model) renderFooter() string {
	style := lipgloss.NewStyle().Foreground(comment).Margin(0, 2)

	// Format: key label • key label
	commands := []string{
		"esc reset",
		"↑↓ nav",
		"s/S select/bulk",
		"N add",
		"E edit",
		"X del",
		"/ search",
		"P export",
		"I import",
		"q quit",
	}

	return style.Render(strings.Join(commands, "  •  "))
}

func (m *model) getStatusText() string {
	switch m.state {
	case stateSearch:
		return "filtering"
	case stateAdd, stateEdit:
		return "editing"
	default:
		return "active"
	}
}

func InitDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	// ADD 'qty' TEXT DEFAULT '0' TO THE QUERY BELOW
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS inventory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		qty TEXT DEFAULT '0' 
	)`)

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS field_definitions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL
	)`)

	return db, err
}

func (m *model) itemsToRows(items []Item, customCols []string) []table.Row {
	var rows []table.Row
	for _, item := range items {
		prefix := "○ "
		if m.selectedRows[item.ID] {
			prefix = "● "
		}

		row := table.Row{
			item.ID,
			prefix + item.Name,
			item.Qty,
		}

		// Only append values for columns that aren't "Green" or "Orange"
		for _, colName := range customCols {
			lower := strings.ToLower(colName)
			if lower == "id" || lower == "name" || lower == "qty" ||
				lower == "green" || lower == "orange" {
				continue
			}
			row = append(row, item.Values[colName])
		}
		rows = append(rows, row)
	}
	return rows
}

func (m *model) refreshData() {
	items, customCols, err := GetInventory(m.db)
	if err != nil {
		m.statusMsg = "Fetch Error: " + err.Error()
		return
	}
	m.inventory = items

	// 1. Calculate widths (Keep your existing math here)
	coreWidth := 24
	remainingWidth := m.width - coreWidth
	nameWidth := int(float64(remainingWidth) * 0.4)
	if nameWidth < 20 {
		nameWidth = 20
	}

	customFieldSpace := remainingWidth - nameWidth
	numCustom := 0
	for _, c := range customCols {
		if c != "id" && c != "name" && c != "qty" {
			numCustom++
		}
	}

	perFieldWidth := 15
	if numCustom > 0 {
		perFieldWidth = customFieldSpace / numCustom
		if perFieldWidth < 10 {
			perFieldWidth = 10
		}
	}

	newHeight := m.height - 7
	if newHeight < 5 {
		newHeight = 5 // Minimum height safety
	}

	m.table.SetHeight(newHeight)
	m.table.SetRows([]table.Row{})

	// 3. Define the new Column structure
	cols := []table.Column{
		{Title: "ID", Width: 6},
		{Title: "NAME", Width: nameWidth},
		{Title: "QTY", Width: 8},
	}
	for _, col := range customCols {
		// Skip system-reserved columns from the TABLE view
		lowerCol := strings.ToLower(col)
		if lowerCol == "id" || lowerCol == "name" || lowerCol == "qty" ||
			lowerCol == "green" || lowerCol == "orange" {
			continue
		}
		cols = append(cols, table.Column{Title: strings.ToUpper(col), Width: 15})
	}

	m.table.SetColumns(cols)
	m.table.SetRows(m.itemsToRows(items, customCols))
}

func (m *model) renderTable() string {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		Bold(true).
		Foreground(comment) // Dim the headers
	s.Selected = s.Selected.
		Background(lipgloss.Color("#44475A")). // Subtle grey highlight
		Foreground(foreground).
		Bold(true)
	m.table.SetStyles(s)
	return m.table.View()
}

func (m *model) handleForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	kmsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Define the key string for easier use
	keyStr := kmsg.String()

	switch keyStr {
	case "esc":
		m.state = stateNav
		return m, nil
	case "tab", "down":
		m.inputs[m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
		m.inputs[m.focusIndex].Focus()
		return m, nil
	case "up":
		m.inputs[m.focusIndex].Blur()
		m.focusIndex--
		if m.focusIndex < 0 {
			m.focusIndex = len(m.inputs) - 1
		}
		m.inputs[m.focusIndex].Focus()
		return m, nil
	case "enter", "ctrl+s":
		var query string
		var args []interface{}
		var err error

		// Determine if this was a Save & Add session
		isCtrlS := keyStr == "ctrl+s"

		// 1. Validate input
		if strings.TrimSpace(m.inputs[0].Value()) == "" {
			return m, m.setStatus("Error: Name cannot be empty")
		}

		// 2. Database Logic
		if m.state == stateEdit {
			// UPDATE Existing Item
			query = "UPDATE inventory SET name = ?"
			args = []interface{}{m.inputs[0].Value()}
			// Start loop at 1 to match fieldNames (Name is 0, Qty is 1, etc.)
			for i := 1; i < len(m.inputs); i++ {
				if i < len(m.fieldNames) {
					query += fmt.Sprintf(", [%s] = ?", m.fieldNames[i])
					args = append(args, m.inputs[i].Value())
				}
			}
			query += " WHERE id = ?"
			args = append(args, m.editTargetID)
		} else {
			// INSERT New Item
			cols := "name"
			vals := "?"
			args = []interface{}{m.inputs[0].Value()}
			for i := 1; i < len(m.inputs); i++ {
				if i < len(m.fieldNames) {
					cols += fmt.Sprintf(", [%s]", m.fieldNames[i])
					vals += ", ?"
					args = append(args, m.inputs[i].Value())
				}
			}
			query = fmt.Sprintf("INSERT INTO inventory (%s) VALUES (%s)", cols, vals)
		}

		// Execute directly using the model's DB connection
		_, err = m.db.Exec(query, args...)
		if err != nil {
			return m, m.setStatus("DB Error: " + err.Error())
		}

		// 3. Refresh and State Guard
		m.refreshData()

		// If ctrl+s was used while ADDING, stay in form and clear fields
		if isCtrlS && m.state == stateAdd {
			m.resetInputs()
			m.inputs[0].Focus()
			return m, m.setStatus("Item Added! Ready for next...")
		}

		// In all other cases (Enter or ctrl+s while editing), go back to list
		m.state = stateNav
		m.table.Focus()
		return m, m.setStatus("Changes Saved")
	}

	// Update the focused input with the key message
	var cmd tea.Cmd
	m.inputs[m.focusIndex], cmd = m.inputs[m.focusIndex].Update(msg)
	return m, cmd
}

func (m *model) handleSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if kmsg, ok := msg.(tea.KeyMsg); ok {
		switch kmsg.String() {
		case "esc", "enter":
			m.state = stateNav
			m.refreshData() // Reset table to full view
			return m, nil
		}
	}

	m.inputs[0], cmd = m.inputs[0].Update(msg)
	searchTerm := m.inputs[0].Value()

	// 1. Prepare search targets (combine all field values for each item)
	var targets []string
	for _, item := range m.inventory {
		searchStr := item.Name + " " + item.Qty
		for _, val := range item.Values {
			searchStr += " " + val
		}
		targets = append(targets, searchStr)
	}

	// 2. Perform fuzzy find
	matches := fuzzy.Find(searchTerm, targets)

	// 3. Reconstruct rows for matches
	var matchedItems []Item
	for _, match := range matches {
		matchedItems = append(matchedItems, m.inventory[match.Index])
	}

	// 4. Update the table display
	_, customCols, _ := GetInventory(m.db)
	if searchTerm == "" {
		m.table.SetRows(m.itemsToRows(m.inventory, customCols))
	} else {
		m.table.SetRows(m.itemsToRows(matchedItems, customCols))
	}

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
			m.state = stateNav
			return m, nil
		case "enter":
			filename := m.inputs[0].Value()
			if filename == "" {
				filename = "export.csv"
			}
			err := m.exportToCSV(filename)
			if err != nil {
				return m, m.setStatus("Export Failed: " + err.Error())
			}
			m.state = stateNav
			return m, m.setStatus("Exported to " + filename)
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
			m.state = stateNav
			return m, nil
		case "enter":
			path := m.inputs[0].Value()
			count, err := m.importFromCSV(path)
			if err != nil {
				return m, m.setStatus("Import Failed: " + err.Error())
			}
			m.refreshData() // Load the new items
			m.state = stateNav
			return m, m.setStatus(fmt.Sprintf("Imported %d items", count))
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
			for id, selected := range m.selectedRows {
				if selected {
					idsToDelete = append(idsToDelete, id)
				}
			}
			if len(idsToDelete) == 0 && m.deleteTargetID != "" {
				idsToDelete = append(idsToDelete, m.deleteTargetID)
			}
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
    [ MAIN SCREEN ] 

    [?]      Toggle Help
    [esc]    Back / Reset
    [q]      Quit
    [/]      Search Items
    [N]      Add New Item
	 -  [ctrl+s]   Save & Create
    [E]      Edit Selected Item
    [X]      Delete Selected Items
    [I]      Import CSV from 'import.csv'
    [P]      Export to CSV

    [F]      [ Field Manager ]
     -  [R]  Rename Field
     -  [A]  Add Field
     -  [X]  Delete Field
     -  [Esc]  Back / Reset

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
	m.focusIndex = 0
	m.editTargetID = ""
	m.deleteTargetID = ""
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.refreshData()
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
	b.WriteString("\n  [tab] cycle • [enter] save • [ctrl+s] save & create • [esc] cancel")
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
	// --- FIX: GET THE MISSING VARIABLES ---
	items, customCols, err := GetInventory(m.db)
	if err != nil {
		return err
	}
	// --------------------------------------

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Create Headers
	headers := []string{"ID", "Name", "Qty"}
	for _, col := range customCols {
		if col != "id" && col != "name" && col != "qty" {
			headers = append(headers, col)
		}
	}
	writer.Write(headers)

	// Write Data
	for _, item := range items {
		row := []string{item.ID, item.Name, item.Qty}
		for _, col := range customCols {
			if col != "id" && col != "name" && col != "qty" {
				row = append(row, item.Values[col])
			}
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
		return 0, fmt.Errorf("CSV is empty")
	}

	headers := records[0]
	// Pre-check: Ensure custom columns exist in DB
	for _, h := range headers {
		lowH := strings.ToLower(h)
		if lowH != "id" && lowH != "name" && lowH != "qty" {
			AddCustomField(m.db, h) // Creates column if missing
		}
	}

	count := 0
	for _, record := range records[1:] {
		cols := []string{}
		placeholders := []string{}
		args := []interface{}{}

		for i, val := range record {
			if i >= len(headers) {
				break
			}
			header := strings.ToLower(headers[i])

			if header == "id" {
				continue
			} // Let SQLite handle ID

			cols = append(cols, fmt.Sprintf("[%s]", headers[i]))
			placeholders = append(placeholders, "?")
			args = append(args, val)
		}

		query := fmt.Sprintf("INSERT INTO inventory (%s) VALUES (%s)",
			strings.Join(cols, ", "),
			strings.Join(placeholders, ", "))

		_, err := m.db.Exec(query, args...)
		if err == nil {
			count++
		}
	}
	return count, nil
}

func (m *model) initDynamicInputs() {
	// 1. Always start with Name and Qty
	m.fieldNames = []string{"Name", "Qty"}

	// 2. Fetch custom fields from DB
	_, customCols, _ := GetInventory(m.db)
	for _, c := range customCols {
		if c != "id" && c != "name" && c != "qty" {
			m.fieldNames = append(m.fieldNames, c)
		}
	}

	// 3. Create inputs for every field name
	m.inputs = make([]textinput.Model, len(m.fieldNames))
	for i, name := range m.fieldNames {
		t := textinput.New()
		t.Placeholder = name
		if i == 0 {
			t.Focus()
		}
		m.inputs[i] = t
	}

	m.inputs = make([]textinput.Model, len(customCols)+2) // Name + Qty + Custom

	for i := range m.inputs {
		t := textinput.New()
		t.Cursor.Style = lipgloss.NewStyle().Foreground(purple)

		// --- ADD THIS LINE ---
		t.Width = 20 // Set a visible window of 30 characters
		// --------------------

		if i == 0 {
			t.Placeholder = "Name"
		} else if i == 1 {
			t.Placeholder = "Quantity"
		}
		// ...
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

	initialCols := []table.Column{
		{Title: "ID", Width: 6},
		{Title: "NAME", Width: 30},
		{Title: "QTY", Width: 8},
	}

	m := model{
		db:           db,
		state:        stateNav,
		selectedRows: make(map[string]bool),
		// Initialize table with empty columns/rows to start
		table: table.New(
			table.WithColumns(initialCols),
			table.WithFocused(true),
		),
	}

	m.refreshData()
	m.initDynamicInputs()

	p := tea.NewProgram(&m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
