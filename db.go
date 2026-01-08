package main

import (
	"database/sql"
	"fmt"
	"github.com/charmbracelet/bubbles/table"
	_ "modernc.org/sqlite" // Pure Go driver
)

func InitDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	// 1. Table for field names (e.g., "Quantity", "Price", "Shelf Location")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS field_definitions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE
	)`)

	// 2. Core inventory table
	// We will start with 'name' as the only permanent column.
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS inventory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	)`)

	return db, nil
}

func GetInventory(db *sql.DB) ([]Item, []string, error) {
	// 1. Get custom column names
	colRows, _ := db.Query("SELECT name FROM field_definitions")
	var customCols []string
	for colRows.Next() {
		var c string
		colRows.Scan(&c)
		customCols = append(customCols, c)
	}
	colRows.Close()

	// 2. Fetch all data
	rows, _ := db.Query("SELECT * FROM inventory")
	defer rows.Close()

	cols, _ := rows.Columns()
	var items []Item

	for rows.Next() {
		// Use a slice of interfaces to scan dynamic columns
		columns := make([]interface{}, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		rows.Scan(columnPointers...)

		item := Item{Values: make(map[string]string)}
		for i, colName := range cols {
			val := fmt.Sprintf("%v", columns[i])
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

func (i Item) ToRow(customCols []string) table.Row {
	row := table.Row{i.ID, i.Name}
	for _, colName := range customCols {
		// Use the map we built in GetInventory
		row = append(row, i.Values[colName])
	}
	return row
}

func DeleteCustomField(db *sql.DB, fieldName string) error {
	// 1. Remove from metadata
	_, err := db.Exec("DELETE FROM field_definitions WHERE name = ?", fieldName)
	if err != nil {
		return err
	}
	// 2. Remove from actual table
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE inventory DROP COLUMN [%s]", fieldName))
	return err
}
