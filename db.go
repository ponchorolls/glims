package main

import (
	"database/sql"
	_ "modernc.org/sqlite" // Pure Go driver
)

func InitDB(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	// Create table if it doesn't exist
	query := `
	CREATE TABLE IF NOT EXISTS inventory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		qty INTEGER DEFAULT 0
	);`

	_, err = db.Exec(query)
	return db, err
}

func GetInventory(db *sql.DB) ([]Item, error) {
	rows, err := db.Query("SELECT id, name, qty FROM inventory")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.ID, &i.Name, &i.Qty); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}
