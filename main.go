package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type sessionState int

const (
	stateNav           sessionState = iota // Moving through the list, pressing hotkeys
	stateSearch                            // Typing into the fuzzy finder
	stateAdd                               // Typing into the "Add Item" form
	stateDeleteConfirm                     // Delete confirmation state
)

// Define our data structure
type Item struct {
	ID   string
	Name string
	Qty  string
}

// Helper to convert our Items to table rows
func (i Item) ToRow() table.Row {
	return table.Row{i.ID, i.Name, i.Qty}
}

type model struct {
	table          table.Model
	textInput      textinput.Model
	qtyInput       textinput.Model
	inventory      []Item // The source of truth
	state          sessionState
	focusIndex     int
	db             *sql.DB
	deleteTargetID string
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

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// 1. Handle Global Keypresses (like Ctrl+C)
	// We check if the message is a KeyMsg first
	if kmsg, ok := msg.(tea.KeyMsg); ok {
		if kmsg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	// 2. Route to Mode-Specific Helpers
	// These helpers return (tea.Model, tea.Cmd) directly
	switch m.state {
	case stateSearch:
		return m.handleSearch(msg)
	case stateAdd:
		return m.handleAdd(msg)
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
			m.textInput.SetValue("")
			m.textInput.Focus()
			return m, nil
		case "n":
			m.state = stateAdd
			m.focusIndex = 0
			m.textInput.SetValue("")
			m.qtyInput.SetValue("")
			m.textInput.Focus()
			return m, nil
		case "d":
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
			m.state = stateNav
			return m, nil
		case "tab", "shift+tab", "up", "down":
			// Cycle focus between Name (0) and Qty (1)
			if m.focusIndex == 0 {
				m.focusIndex = 1
				m.textInput.Blur()
				m.qtyInput.Focus()
			} else {
				m.focusIndex = 0
				m.qtyInput.Blur()
				m.textInput.Focus()
			}
			return m, nil
		case "enter":
			// SAVE TO DB
			name := m.textInput.Value()
			qty := m.qtyInput.Value()

			if name != "" {
				_, _ = m.db.Exec("INSERT INTO inventory (name, qty) VALUES (?, ?)", name, qty)
				// Refresh local list from DB
				m.inventory, _ = GetInventory(m.db)

				// Reset table rows
				var newRows []table.Row
				for _, item := range m.inventory {
					newRows = append(newRows, item.ToRow())
				}
				m.table.SetRows(newRows)
			}

			m.state = stateNav
			return m, nil
		}
	}

	// Update only the input that has focus
	if m.focusIndex == 0 {
		m.textInput, cmd = m.textInput.Update(msg)
	} else {
		m.qtyInput, cmd = m.qtyInput.Update(msg)
	}

	return m, cmd
}

func (m *model) handleSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "enter" || msg.String() == "esc" {
			m.state = stateNav
			m.textInput.Blur()
			return m, nil
		}
	}

	m.textInput, cmd = m.textInput.Update(msg)

	// Filter inventory
	var targets []string
	for _, item := range m.inventory {
		targets = append(targets, item.Name)
	}
	matches := fuzzy.Find(m.textInput.Value(), targets)

	var newRows []table.Row
	if m.textInput.Value() == "" {
		for _, item := range m.inventory {
			newRows = append(newRows, item.ToRow())
		}
	} else {
		for _, match := range matches {
			newRows = append(newRows, m.inventory[match.Index].ToRow())
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
			m.inventory, _ = GetInventory(m.db)

			// Refresh table
			var rows []table.Row
			for _, item := range m.inventory {
				rows = append(rows, item.ToRow())
			}
			m.table.SetRows(rows)

			m.state = stateNav
			return m, nil

		case "n", "N", "esc":
			m.state = stateNav // Cancel
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
		// Show the search input above the table
		currentView = fmt.Sprintf(
			"%s\n\n%s",
			m.textInput.View(),
			baseStyle.Render(m.table.View()),
		)

	case stateAdd:
		statusLine = lipgloss.NewStyle().Background(green).Foreground(lipgloss.Color("0")).Bold(true).Render(" ADD ITEM ")
		// Show the Form instead of the table
		currentView = fmt.Sprintf(
			"\n  Product Name:\n  %s\n\n  Quantity:\n  %s\n\n  (enter to save • esc to cancel)",
			m.textInput.View(),
			m.qtyInput.View(),
		)

	case stateDeleteConfirm:
		statusLine = lipgloss.NewStyle().Background(red).Foreground(lipgloss.Color("15")).Bold(true).Render(" CONFIRM DELETE ")
		currentView = fmt.Sprintf(
			"\n  Are you sure you want to delete item #%s?\n\n  [y] Yes  •  [n] No",
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
		footerStyle.Render(" [/] Search • [n] New • [d] Delete • [esc] Reset • [q/ctrl+c] Quit"),
	)
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

func (m *model) resetToNav() {
	m.state = stateNav
	m.textInput.Blur()
	m.qtyInput.Blur()
	m.textInput.SetValue("")
	m.qtyInput.SetValue("")

	// Ensure the table shows the full, unfiltered inventory
	var rows []table.Row
	for _, item := range m.inventory {
		rows = append(rows, item.ToRow())
	}
	m.table.SetRows(rows)
}

// func main() {
// 	// Initialize Database
// 	db, err := InitDB("glims.db")
// 	if err != nil {
// 		fmt.Printf("Database error: %v\n", err)
// 		os.Exit(1)
// 	}
// 	defer db.Close()

// 	// Load items from DB
// 	// initialInventory, err := GetInventory(db)
// 	// if err != nil {
// 	// 	fmt.Printf("Error loading data: %v\n", err)
// 	// 	os.Exit(1)
// 	// }
// 	initialInventory, _ := GetInventory(db)

// 	var rows []table.Row
// 	for _, item := range initialInventory {
// 		rows = append(rows, item.ToRow())
// 	}
// 	items, _ := GetInventory(db)

// 	// Setup Table
// 	columns := []table.Column{
// 		{Title: "ID", Width: 4},
// 		{Title: "Item Name", Width: 30},
// 		{Title: "Quantity", Width: 10},
// 	}

// 	t := table.New(
// 		table.WithColumns(columns),
// 		table.WithFocused(true),
// 		table.WithHeight(10),
// 	)

// 	s := table.DefaultStyles()
// 	s.Header = s.Header.
// 		BorderStyle(lipgloss.NormalBorder()).
// 		BorderForeground(lipgloss.Color("240")).
// 		BorderBottom(true).
// 		Bold(true)
// 	s.Selected = s.Selected.
// 		Foreground(lipgloss.Color("229")).
// 		Background(lipgloss.Color("57")).
// 		Bold(true)
// 	t.SetStyles(s)

// 	ti := textinput.New()
// 	ti.Placeholder = "Enter Name..."
// 	ti.Focus()
// 	ti.CharLimit = 156
// 	ti.Width = 30

// 	qi := textinput.New() // Ensure this is called!
// 	qi.Placeholder = "Enter Qty..."
// 	qi.CharLimit = 10
// 	qi.Width = 10

// 	m := &model{ // Use & to create a pointer to the model
// 		table:     t,
// 		inventory: initialInventory,
// 		textInput: ti,
// 		qtyInput:  qi,
// 		db:        db,
// 		state:     stateNav,
// 	}

// 	t.SetRows(rows)

//		// Bubble Tea handles the pointer automatically
//		if _, err := tea.NewProgram(m).Run(); err != nil {
//			fmt.Println(err)
//		}
//	}
func main() {
	db, err := InitDB("glims.db")
	if err != nil {
		fmt.Printf("Database error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// 1. Get the data from SQLite
	items, err := GetInventory(db) // 'items' is declared here
	if err != nil {
		fmt.Printf("Error loading data: %v\n", err)
		os.Exit(1)
	}

	// 2. Setup Table columns
	columns := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Item Name", Width: 30},
		{Title: "Quantity", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	// 3. USE 'items' to populate the table
	var rows []table.Row
	for _, item := range items {
		rows = append(rows, item.ToRow())
	}
	t.SetRows(rows)

	// 4. USE 'items' again to initialize the Model
	m := &model{
		table:     t,
		inventory: items, // Pass items into the model here
		db:        db,
		state:     stateNav,
		textInput: textinput.New(), // Make sure these are initialized
		qtyInput:  textinput.New(),
	}

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)
	t.SetStyles(s)

	// Now 'items' has been used in two places, and the error will vanish.
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}
